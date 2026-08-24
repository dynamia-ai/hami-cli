/*
Copyright 2026 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	sigsyaml "sigs.k8s.io/yaml"

	resourcepoolv1alpha1 "github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api/v1alpha1"
)

func TestRootCommandUsesKubectlPluginDisplayNameAndStandardFlags(t *testing.T) {
	t.Parallel()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{out: output, errOut: output, executable: "/tmp/kubectl-hami_cli"})
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.String(), "kubectl hami-cli") {
		t.Fatalf("help output = %q, want kubectl plugin display name", output.String())
	}
	for _, name := range []string{"kubeconfig", "context", "namespace", "request-timeout", "as", "as-group", "server"} {
		if command.PersistentFlags().Lookup(name) == nil {
			t.Errorf("missing standard Kubernetes flag --%s", name)
		}
	}
}

func TestListCommandsRejectPositionalArguments(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, args := range [][]string{
		{"pool", "list", "training-a"},
		{"device", "list", "node-a"},
	} {
		command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
		command.SetArgs(args)
		err := command.Execute()
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("unknown command %q", args[2])) {
			t.Errorf("%v error = %v, want positional argument rejection", args, err)
		}
	}
}

func TestUnstructuredPoolNamesUseResourceQualifiedName(t *testing.T) {
	t.Parallel()
	pools := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{{Object: map[string]any{"metadata": map[string]any{"name": "training-a"}}}},
	}
	want := []string{"resourcepool.resource.hami.io/training-a"}
	if got := unstructuredPoolNames(pools); !slices.Equal(got, want) {
		t.Errorf("unstructuredPoolNames() = %v, want %v", got, want)
	}
}

func TestPoolListStructuredOutputPreservesExtensionFields(t *testing.T) {
	t.Parallel()
	pool := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": resourcepoolv1alpha1.GroupVersion.String(),
		"kind":       "ResourcePool",
		"metadata":   map[string]any{"name": "training-a"},
		"spec": map[string]any{"workloadDefaults": map[string]any{
			"schedulingPolicy": map[string]any{"node": "spread", "gpu": "binpack,numa"},
		}},
	}}
	cli := fake.NewClientBuilder().WithObjects(pool).Build()
	for _, outputFormat := range []string{"json", "yaml"} {
		t.Run(outputFormat, func(t *testing.T) {
			output := &bytes.Buffer{}
			options := &commandOptions{client: cli, out: output, errOut: output}
			command := newRootCommand(options)
			command.SetArgs([]string{"pool", "list", "-o", outputFormat})
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			listed := &unstructured.UnstructuredList{}
			if err := sigsyaml.Unmarshal(output.Bytes(), listed); err != nil {
				t.Fatalf("decode %s output: %v", outputFormat, err)
			}
			if len(listed.Items) != 1 {
				t.Fatalf("listed pools = %d, want 1", len(listed.Items))
			}
			node, found, err := unstructured.NestedString(listed.Items[0].Object, "spec", "workloadDefaults", "schedulingPolicy", "node")
			if err != nil || !found || node != "spread" {
				t.Fatalf("listed node policy = %q, found = %v, error = %v", node, found, err)
			}
		})
	}
}

func TestBindingListHonorsNamespaceAndAllNamespaces(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objects := []client.Object{
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-b", Namespace: "team-b"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: "pool-b"}},
		},
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-a", Namespace: "team-a"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: "pool-a"}},
		},
	}
	newOptions := func() (*commandOptions, *bytes.Buffer) {
		output := &bytes.Buffer{}
		overrides := &clientcmd.ConfigOverrides{}
		rawConfig := clientcmdapi.Config{
			CurrentContext: "test",
			Contexts: map[string]*clientcmdapi.Context{
				"test": {Namespace: "default"},
			},
		}
		return &commandOptions{
			overrides:    overrides,
			clientConfig: clientcmd.NewDefaultClientConfig(rawConfig, overrides),
			client:       fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
			out:          output,
			errOut:       output,
		}, output
	}

	options, output := newOptions()
	command := newRootCommand(options)
	command.SetArgs([]string{"binding", "list", "--namespace", "team-a", "-o", "name"})
	if err := command.Execute(); err != nil {
		t.Fatalf("namespaced binding list error = %v", err)
	}
	if got, want := strings.TrimSpace(output.String()), "resourcepoolbinding.resource.hami.io/pool-a"; got != want {
		t.Fatalf("namespaced binding list = %q, want %q", got, want)
	}

	options, output = newOptions()
	command = newRootCommand(options)
	command.SetArgs([]string{"binding", "list", "--all-namespaces", "-o", "name"})
	if err := command.Execute(); err != nil {
		t.Fatalf("all-namespaces binding list error = %v", err)
	}
	if got, want := output.String(), "resourcepoolbinding.resource.hami.io/pool-a\nresourcepoolbinding.resource.hami.io/pool-b\n"; got != want {
		t.Fatalf("all-namespaces binding list = %q, want %q", got, want)
	}

	conflicts := [][]string{
		{"binding", "list", "--namespace", "team-a", "--all-namespaces"},
		{"binding", "list", "-n", "team-a", "-A"},
		{"--namespace", "team-a", "binding", "list", "--all-namespaces"},
		{"binding", "ls", "--namespace", "team-a", "--all-namespaces"},
	}
	for _, args := range conflicts {
		options, _ = newOptions()
		command = newRootCommand(options)
		command.SetArgs(args)
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Errorf("%v error = %v, want namespace conflict rejection", args, err)
		}
	}

	options, output = newOptions()
	command = newRootCommand(options)
	command.SetArgs([]string{"binding", "list", "--namespace", "team-a", "--all-namespaces=false", "-o", "name"})
	if err := command.Execute(); err != nil {
		t.Fatalf("explicit false all-namespaces error = %v", err)
	}
	if got, want := strings.TrimSpace(output.String()), "resourcepoolbinding.resource.hami.io/pool-a"; got != want {
		t.Fatalf("explicit false all-namespaces result = %q, want %q", got, want)
	}
}

func TestBindAndUnbindAreRootCommands(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	newOptions := func() *commandOptions {
		overrides := &clientcmd.ConfigOverrides{}
		rawConfig := clientcmdapi.Config{
			CurrentContext: "test",
			Contexts: map[string]*clientcmdapi.Context{
				"test": {Namespace: "team-a"},
			},
		}
		return &commandOptions{
			overrides:    overrides,
			clientConfig: clientcmd.NewDefaultClientConfig(rawConfig, overrides),
			client:       cli,
			out:          &bytes.Buffer{},
			errOut:       &bytes.Buffer{},
		}
	}

	command := newRootCommand(newOptions())
	command.SetArgs([]string{"bind", "--namespace", "team-a", "--pool", "training-a"})
	if err := command.Execute(); err != nil {
		t.Fatalf("root bind error = %v", err)
	}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{}
	key := client.ObjectKey{Name: "training-a", Namespace: "team-a"}
	if err := cli.Get(t.Context(), key, binding); err != nil {
		t.Fatalf("get created binding: %v", err)
	}

	command = newRootCommand(newOptions())
	command.SetArgs([]string{"unbind", "--namespace", "team-a", "--pool", "training-a"})
	if err := command.Execute(); err != nil {
		t.Fatalf("root unbind error = %v", err)
	}
	if err := cli.Get(t.Context(), key, binding); !apierrors.IsNotFound(err) {
		t.Fatalf("get deleted binding error = %v, want NotFound", err)
	}

	command = newRootCommand(newOptions())
	command.SetArgs([]string{"binding", "bind", "--pool", "training-a"})
	if err := command.Execute(); err == nil {
		t.Fatal("nested binding bind succeeded, want rejected route")
	}
	bindingCommand, _, err := newRootCommand(newOptions()).Find([]string{"binding"})
	if err != nil {
		t.Fatalf("find binding command: %v", err)
	}
	for _, child := range bindingCommand.Commands() {
		if child.Name() == "bind" || child.Name() == "unbind" {
			t.Fatalf("binding child %q remains registered", child.Name())
		}
	}
}

func TestPoolListNameOutput(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "training-a"}},
		).Build(),
		out: output, errOut: output,
	})
	command.SetArgs([]string{"pool", "list", "-o", "name"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool list error = %v", err)
	}
	if got, want := strings.TrimSpace(output.String()), "resourcepool.resource.hami.io/training-a"; got != want {
		t.Fatalf("pool list = %q, want %q", got, want)
	}
}

func TestDefaultApplyNamespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, manifest, context, override, want, wantErr string
	}{
		{name: "explicit override", override: "team-a", want: "team-a"},
		{name: "kubeconfig context", context: "team-a", want: "team-a"},
		{name: "manifest namespace", manifest: "team-b", context: "team-a", want: "team-b"},
		{name: "matching explicit namespace", manifest: "team-a", override: "team-a", want: "team-a"},
		{name: "override conflict", manifest: "team-b", override: "team-a", want: "team-b", wantErr: "does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			overrides := &clientcmd.ConfigOverrides{}
			overrides.Context.Namespace = test.override
			options := &commandOptions{clientConfig: clientcmd.NewDefaultClientConfig(clientcmdapi.Config{
				CurrentContext: "test",
				Clusters: map[string]*clientcmdapi.Cluster{
					"test": {Server: "https://example.invalid"},
				},
				Contexts: map[string]*clientcmdapi.Context{
					"test": {Cluster: "test", Namespace: test.context},
				},
			}, overrides)}
			binding := &resourcepoolv1alpha1.ResourcePoolBinding{ObjectMeta: metav1.ObjectMeta{Namespace: test.manifest}}
			err := options.defaultApplyNamespace(binding)
			if test.wantErr == "" && err != nil {
				t.Fatalf("defaultApplyNamespace() error = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("defaultApplyNamespace() error = %v, want %q", err, test.wantErr)
			}
			if binding.Namespace != test.want {
				t.Errorf("binding namespace = %q, want %q", binding.Namespace, test.want)
			}
		})
	}
}

func TestDeviceAssignInputForms(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"}
	tests := []struct {
		name, deviceName string
		args             []string
	}{
		{name: "object name", deviceName: "device-object", args: []string{"device", "assign", "device-object", "--pool", "training-a"}},
		{name: "physical identity", deviceName: resourcepoolv1alpha1.DeviceObjectName(identity), args: []string{"device", "assign", "--node", "node-a", "--device-id", "GPU-A", "--pool", "training-a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			device := &resourcepoolv1alpha1.GPUDevice{ObjectMeta: metav1.ObjectMeta{Name: test.deviceName, ResourceVersion: "1"}, Spec: resourcepoolv1alpha1.GPUDeviceSpec{Identity: identity}}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device).Build()
			output := &bytes.Buffer{}
			command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			updated := &resourcepoolv1alpha1.GPUDevice{}
			if err := cli.Get(t.Context(), client.ObjectKey{Name: device.Name}, updated); err != nil {
				t.Fatal(err)
			}
			if updated.Spec.PoolRef == nil || updated.Spec.PoolRef.Name != "training-a" {
				t.Fatalf("updated pool = %#v", updated.Spec.PoolRef)
			}
		})
	}

	invalid := []struct {
		args []string
		want string
	}{
		{args: []string{"device", "assign", "--node", "node-a", "--pool", "training-a"}, want: "both --node and --device-id"},
		{args: []string{"device", "assign", "--device-id", "GPU-A", "--pool", "training-a"}, want: "both --node and --device-id"},
		{args: []string{"device", "assign", "device-object", "--node", "node-a", "--device-id", "GPU-A", "--pool", "training-a"}, want: "cannot be combined"},
	}
	for _, test := range invalid {
		command := newRootCommand(&commandOptions{client: fake.NewClientBuilder().WithScheme(scheme).Build(), out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
		command.SetArgs(test.args)
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Execute(%v) error = %v, want %q", test.args, err, test.want)
		}
	}
}

func TestDecodeResourcePoolObjectRejectsUnknownTypedFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "GPUDevice",
			raw:  `{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice","metadata":{"name":"device-a","resourceVersion":"7"},"spec":{"identity":{"nodeName":"node-a","deviceType":"NVIDIA","deviceID":"GPU-A"},"poolReff":{"name":"training-a"}}}`,
		},
		{
			name: "ResourcePoolBinding",
			raw:  `{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePoolBinding","metadata":{"name":"training-a","namespace":"team-a"},"spec":{"poolReff":{"name":"training-a"}}}`,
		},
		{
			name: "nested pool reference",
			raw:  `{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice","metadata":{"name":"device-a","resourceVersion":"7"},"spec":{"identity":{"nodeName":"node-a","deviceType":"NVIDIA","deviceID":"GPU-A"},"poolRef":{"name":"training-a","naem":"training-b"}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeResourcePoolObject([]byte(test.raw))
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("decodeResourcePoolObject() error = %v, want unknown field rejection", err)
			}
		})
	}
}

func TestApplyRejectsUnknownGPUDeviceYAMLField(t *testing.T) {
	t.Parallel()
	manifest := `apiVersion: resource.hami.io/v1alpha1
kind: GPUDevice
metadata:
  name: device-a
  resourceVersion: "7"
spec:
  identity:
    nodeName: node-a
    deviceType: NVIDIA
    deviceID: GPU-A
  poolReff:
    name: training-a
`
	path := filepath.Join(t.TempDir(), "device.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().Build(),
		out:    output,
		errOut: output,
	})
	command.SetArgs([]string{"apply", "-f", path})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), `unknown field "poolReff"`) {
		t.Fatalf("apply error = %v, want unknown field rejection", err)
	}
}

func TestDecodeObjectRejectsTrailingJSONValue(t *testing.T) {
	t.Parallel()
	object := &resourcepoolv1alpha1.GPUDevice{}
	err := decodeObject([]byte(`{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice"} {}`), object, true)
	if err == nil || !strings.Contains(err.Error(), "unexpected trailing JSON value") {
		t.Fatalf("decodeObject() error = %v, want trailing JSON rejection", err)
	}
}

func TestDecodeResourcePoolObjectPreservesUnknownPoolFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePool","metadata":{"name":"training-a"},"spec":{"futureField":{"enabled":true}}}`)
	object, err := decodeResourcePoolObject(raw)
	if err != nil {
		t.Fatalf("decodeResourcePoolObject() error = %v", err)
	}
	pool, ok := object.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("object type = %T, want *unstructured.Unstructured", object)
	}
	enabled, found, err := unstructured.NestedBool(pool.Object, "spec", "futureField", "enabled")
	if err != nil || !found || !enabled {
		t.Fatalf("future field enabled = %t, found = %t, error = %v", enabled, found, err)
	}
}

func TestDecodeAndApplyResourcePoolPreservesWorkloadDefaults(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePool","metadata":{"name":"training-a"},"spec":{"workloadDefaults":{"schedulingPolicy":{"node":"spread","gpu":"binpack,numa"}}}}`)
	object, err := decodeResourcePoolObject(raw)
	if err != nil {
		t.Fatalf("decode ResourcePool error = %v", err)
	}
	pool, ok := object.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("object type = %T, want *unstructured.Unstructured", object)
	}
	nodePolicy, found, err := unstructured.NestedString(pool.Object, "spec", "workloadDefaults", "schedulingPolicy", "node")
	if err != nil || !found || nodePolicy != "spread" {
		t.Fatalf("decoded node policy = %q, found = %v, error = %v", nodePolicy, found, err)
	}

	baseClient := fake.NewClientBuilder().Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(_ context.Context, _ client.WithWatch, patched client.Object, patch client.Patch, _ ...client.PatchOption) error {
			data, err := patch.Data(patched)
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), `"workloadDefaults":{"schedulingPolicy":{"gpu":"binpack,numa","node":"spread"}}`) {
				t.Fatalf("apply patch = %s, want workload scheduling defaults", data)
			}
			return nil
		},
	})
	if err := applyResourcePoolObject(t.Context(), cli, object); err != nil {
		t.Fatalf("apply ResourcePool error = %v", err)
	}
}

func TestDecodeResourcePoolObjectAddsManagedLabels(t *testing.T) {
	t.Parallel()
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"}
	deviceRaw := fmt.Appendf(nil, `{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice","metadata":{"name":"%s"},"spec":{"identity":{"nodeName":"node-a","deviceType":"NVIDIA","deviceID":"GPU-A"},"poolRef":{"name":"training-a"}}}`, resourcepoolv1alpha1.DeviceObjectName(identity))
	bindingRaw := []byte(`{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePoolBinding","metadata":{"name":"training-a","namespace":"team-a"},"spec":{"poolRef":{"name":"training-a"}}}`)

	tests := []struct {
		name     string
		raw      []byte
		wantType client.Object
		want     map[string]string
	}{
		{name: "GPUDevice", raw: deviceRaw, wantType: &resourcepoolv1alpha1.GPUDevice{}, want: map[string]string{
			resourcepoolv1alpha1.PoolLabel: "training-a", resourcepoolv1alpha1.DeviceTypeLabel: "nvidia",
		}},
		{name: "ResourcePoolBinding", raw: bindingRaw, wantType: &resourcepoolv1alpha1.ResourcePoolBinding{}, want: map[string]string{
			resourcepoolv1alpha1.PoolLabel: "training-a",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object, err := decodeResourcePoolObject(test.raw)
			if err != nil {
				t.Fatalf("decodeResourcePoolObject() error = %v", err)
			}
			if got, want := reflect.TypeOf(object), reflect.TypeOf(test.wantType); got != want {
				t.Fatalf("object type = %v, want %v", got, want)
			}
			for key, want := range test.want {
				if got := object.GetLabels()[key]; got != want {
					t.Errorf("label %q = %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestApplyGPUDeviceUsesManifestResourceVersionAsOptimisticPrecondition(t *testing.T) {
	t.Parallel()
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"}
	current := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: resourcepoolv1alpha1.DeviceObjectName(identity), ResourceVersion: "7"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: identity,
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: "current-pool"},
		},
	}
	current.Labels = resourcepoolv1alpha1.PoolLabels(current)

	tests := []struct {
		name     string
		version  string
		pool     string
		wantErr  string
		wantPool string
	}{
		{
			name:     "missing resource version is rejected",
			pool:     "new-pool",
			wantErr:  "metadata.resourceVersion",
			wantPool: "current-pool",
		},
		{
			name:     "stale resource version reports current pool",
			version:  "6",
			pool:     "new-pool",
			wantErr:  "current-pool",
			wantPool: "current-pool",
		},
		{
			name:     "current resource version changes pool",
			version:  "7",
			pool:     "new-pool",
			wantPool: "new-pool",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(current.DeepCopy()).Build()
			requested := current.DeepCopy()
			requested.ResourceVersion = test.version
			requested.Spec.PoolRef = &resourcepoolv1alpha1.PoolReference{Name: test.pool}
			requested.Labels = resourcepoolv1alpha1.PoolLabels(requested)

			err := applyResourcePoolObject(context.Background(), cli, requested)
			if test.wantErr == "" && err != nil {
				t.Fatalf("applyResourcePoolObject() error = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("applyResourcePoolObject() error = %v, want substring %q", err, test.wantErr)
			}
			got := &resourcepoolv1alpha1.GPUDevice{}
			if err := cli.Get(context.Background(), client.ObjectKey{Name: current.Name}, got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.PoolRef == nil || got.Spec.PoolRef.Name != test.wantPool {
				t.Fatalf("stored pool = %#v, want %q", got.Spec.PoolRef, test.wantPool)
			}
		})
	}
}

func TestApplyGPUDeviceRejectsIdentityMismatch(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"}
	current := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: resourcepoolv1alpha1.DeviceObjectName(identity), ResourceVersion: "7"},
		Spec:       resourcepoolv1alpha1.GPUDeviceSpec{Identity: identity},
	}
	requested := current.DeepCopy()
	requested.Spec.Identity.DeviceID = "GPU-B"
	requested.Spec.PoolRef = &resourcepoolv1alpha1.PoolReference{Name: "new-pool"}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(current).Build()

	err := applyResourcePoolObject(context.Background(), cli, requested)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("applyResourcePoolObject() error = %v, want identity mismatch", err)
	}
	got := &resourcepoolv1alpha1.GPUDevice{}
	if err := cli.Get(context.Background(), client.ObjectKey{Name: current.Name}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.PoolRef != nil {
		t.Fatalf("stored pool = %#v, want unchanged unassigned device", got.Spec.PoolRef)
	}
}

func TestApplyGPUDeviceRejectsConcurrentAssignment(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"}
	current := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: resourcepoolv1alpha1.DeviceObjectName(identity), ResourceVersion: "7"},
		Spec:       resourcepoolv1alpha1.GPUDeviceSpec{Identity: identity},
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(current).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(ctx context.Context, _ client.WithWatch, object client.Object, patch client.Patch, _ ...client.PatchOption) error {
			data, err := patch.Data(object)
			if err != nil || !strings.Contains(string(data), `"resourceVersion":"7"`) {
				t.Fatalf("optimistic patch = %s, %v", data, err)
			}
			latest := current.DeepCopy()
			latest.Spec.PoolRef = &resourcepoolv1alpha1.PoolReference{Name: "concurrent-pool"}
			latest.Labels = resourcepoolv1alpha1.PoolLabels(latest)
			if err := baseClient.Update(ctx, latest); err != nil {
				t.Fatalf("simulate concurrent update: %v", err)
			}
			return apierrors.NewConflict(schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "gpudevices"}, object.GetName(), fmt.Errorf("concurrent update"))
		},
	})
	requested := current.DeepCopy()
	requested.Spec.PoolRef = &resourcepoolv1alpha1.PoolReference{Name: "new-pool"}

	err := applyResourcePoolObject(t.Context(), cli, requested)
	if !apierrors.IsConflict(err) || !strings.Contains(err.Error(), "current pool is concurrent-pool") {
		t.Fatalf("applyResourcePoolObject() error = %v, want current concurrent assignment", err)
	}
}

func TestApplyGPUDeviceCannotCreateControllerManagedInventory(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"}
	requested := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: resourcepoolv1alpha1.DeviceObjectName(identity)},
		Spec:       resourcepoolv1alpha1.GPUDeviceSpec{Identity: identity},
	}
	err := applyResourcePoolObject(context.Background(), fake.NewClientBuilder().WithScheme(scheme).Build(), requested)
	if err == nil || !strings.Contains(err.Error(), "controller-managed") {
		t.Fatalf("applyResourcePoolObject() error = %v, want controller-managed rejection", err)
	}
}
