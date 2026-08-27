package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

func TestSkillInstallCopiesEmbeddedResourcePoolSkill(t *testing.T) {
	home := t.TempDir()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		out:       output,
		errOut:    output,
		kitupHome: home,
		kitupCWD:  t.TempDir(),
	})
	command.SetArgs([]string{"skill", "install", "--scope", "user", "--agent", "codex", "--yes"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output = %q", err, output.String())
	}

	target := filepath.Join(home, ".agents", "skills", "managing-hami-resource-pools")
	for _, path := range []string{
		"SKILL.md",
		filepath.Join("agents", "openai.yaml"),
		filepath.Join("references", "operations.md"),
	} {
		if _, err := os.Stat(filepath.Join(target, path)); err != nil {
			t.Errorf("installed skill file %q: %v", path, err)
		}
	}

	metadataBytes, err := os.ReadFile(filepath.Join(target, ".kitup.json"))
	if err != nil {
		t.Fatalf("read kitup metadata: %v", err)
	}
	metadata := struct {
		SchemaVersion int    `json:"schemaVersion"`
		AppID         string `json:"appId"`
		SkillName     string `json:"skillName"`
		Source        string `json:"source"`
	}{}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("decode kitup metadata: %v", err)
	}
	if metadata.SchemaVersion != 1 || metadata.AppID != "hami-cli" || metadata.SkillName != "managing-hami-resource-pools" || metadata.Source != "bundled" {
		t.Fatalf("kitup metadata = %+v", metadata)
	}
}

func TestPoolListStructuredOutputPreservesExtensionFields(t *testing.T) {
	t.Parallel()
	pool := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": resourcepoolv1alpha1.GroupVersion.String(),
		"kind":       "ResourcePool",
		"metadata":   map[string]any{"name": "training-a"},
		"spec": map[string]any{
			"quota": map[string]any{"cpu": "128", "memory": "512Gi"},
			"workloadDefaults": map[string]any{
				"schedulingPolicy": map[string]any{"node": "spread", "gpu": "binpack,numa"},
			},
		},
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
			cpu, found, err := unstructured.NestedString(listed.Items[0].Object, "spec", "quota", "cpu")
			if err != nil || !found || cpu != "128" {
				t.Fatalf("listed CPU quota = %q, found = %v, error = %v", cpu, found, err)
			}
		})
	}
}

func TestPoolCreateSetsRequestedQuotas(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"pool", "create", "training-a", "--cpu-quota", "128", "--memory-quota", "512Gi"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool create error = %v", err)
	}

	pool := &resourcepoolv1alpha1.ResourcePool{}
	if err := cli.Get(t.Context(), client.ObjectKey{Name: "training-a"}, pool); err != nil {
		t.Fatalf("get created pool: %v", err)
	}
	if pool.Spec.Quota == nil || pool.Spec.Quota.CPU == nil || pool.Spec.Quota.CPU.Cmp(apiresource.MustParse("128")) != 0 {
		t.Fatalf("created CPU quota = %#v, want 128", pool.Spec.Quota)
	}
	if pool.Spec.Quota.Memory == nil || pool.Spec.Quota.Memory.Cmp(apiresource.MustParse("512Gi")) != 0 {
		t.Fatalf("created memory quota = %#v, want 512Gi", pool.Spec.Quota)
	}
	if got, want := output.String(), "resourcepool.resource.hami.io/training-a created\n"; got != want {
		t.Fatalf("pool create output = %q, want %q", got, want)
	}

	command = newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "create", "blocked", "--cpu-quota", "0"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool create with zero quota error = %v", err)
	}
	zeroPool := &resourcepoolv1alpha1.ResourcePool{}
	if err := cli.Get(t.Context(), client.ObjectKey{Name: "blocked"}, zeroPool); err != nil {
		t.Fatalf("get zero-quota pool: %v", err)
	}
	if zeroPool.Spec.Quota == nil || zeroPool.Spec.Quota.CPU == nil || !zeroPool.Spec.Quota.CPU.IsZero() {
		t.Fatalf("created zero CPU quota = %#v, want explicit zero hard ceiling", zeroPool.Spec.Quota)
	}
}

func TestPoolQuotaHelpDescribesLimitCeilings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "create",
			args: []string{"pool", "create", "--help"},
			want: []string{
				"Optional aggregate CPU limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling",
				"Optional aggregate memory limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling",
			},
		},
		{
			name: "update",
			args: []string{"pool", "update", "--help"},
			want: []string{
				"Aggregate CPU limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling, unlimited removes the limit",
				"Aggregate memory limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling, unlimited removes the limit",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output := &bytes.Buffer{}
			command := newRootCommand(&commandOptions{out: output, errOut: output})
			command.SetOut(output)
			command.SetErr(output)
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("help output = %q, want substring %q", output.String(), want)
				}
			}
		})
	}
}

func TestPoolCreateValidatesQuotaBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid CPU", args: []string{"--cpu-quota", "lots"}, want: `invalid CPU quota "lots"`},
		{name: "negative memory", args: []string{"--memory-quota", "-1Gi"}, want: "memory quota must not be negative"},
		{name: "create cannot remove quota", args: []string{"--cpu-quota", "unlimited"}, want: `CPU quota "unlimited" is only valid for pool update`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := newRootCommand(&commandOptions{
				client: fake.NewClientBuilder().Build(),
				out:    &bytes.Buffer{},
				errOut: &bytes.Buffer{},
			})
			command.SetArgs(append([]string{"pool", "create", "training-a"}, test.args...))
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pool create error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDeviceListTableAlignsLongValues(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-d5d6dc2483b9831539b52b8ba053dc3d"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{
				NodeName: "izbp13trb08in2bs1nduw7z",
				DeviceID: "GPU-ceee905d-48ac-93de-a81b-17c00e1e5e02",
			},
			PoolRef: &resourcepoolv1alpha1.PoolReference{Name: "shared-gpu"},
		},
	}
	device.Status.Discovered = true
	device.Status.Healthy = false

	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(device).Build(),
		out:    output, errOut: output,
	})
	command.SetArgs([]string{"device", "list"})
	if err := command.Execute(); err != nil {
		t.Fatalf("device list error = %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("device list output has %d lines, want 2: %q", len(lines), output.String())
	}
	if strings.Contains(output.String(), "\t") {
		t.Fatalf("device list output contains unexpanded tabs: %q", output.String())
	}
	headings := []string{"NAME", "NODE", "DEVICE-ID", "POOL", "DISCOVERED", "HEALTHY"}
	values := []string{device.Name, device.Spec.Identity.NodeName, device.Spec.Identity.DeviceID, device.Spec.PoolRef.Name, "true", "false"}
	for i := range headings {
		if got, want := strings.Index(lines[1], values[i]), strings.Index(lines[0], headings[i]); got != want {
			t.Errorf("column %s starts at %d in data row, want header position %d; output:\n%s", headings[i], got, want, output.String())
		}
	}
}

func TestPoolBindingsListsOnlyTargetPoolAcrossNamespaces(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objects := []client.Object{
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-a", Namespace: "team-b"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: "pool-a"}},
			Status: resourcepoolv1alpha1.ResourcePoolBindingStatus{Conditions: []metav1.Condition{
				{Type: "Bound", Status: metav1.ConditionFalse},
			}},
		},
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-a", Namespace: "team-a"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: "pool-a"}},
			Status: resourcepoolv1alpha1.ResourcePoolBindingStatus{Conditions: []metav1.Condition{
				{Type: "Bound", Status: metav1.ConditionTrue},
			}},
		},
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-b", Namespace: "team-c"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: "pool-b"}},
		},
	}
	newOptions := func() (*commandOptions, *bytes.Buffer) {
		output := &bytes.Buffer{}
		return &commandOptions{
			client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
			out:    output,
			errOut: output,
		}, output
	}

	options, output := newOptions()
	command := newRootCommand(options)
	command.SetArgs([]string{"pool", "bindings", "pool-a"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool bindings table error = %v", err)
	}
	wantTable := "" +
		"NAMESPACE  BINDING  BOUND\n" +
		"team-a     pool-a   True\n" +
		"team-b     pool-a   False\n"
	if got := output.String(); got != wantTable {
		t.Fatalf("pool bindings table = %q, want %q", got, wantTable)
	}

	options, output = newOptions()
	command = newRootCommand(options)
	command.SetArgs([]string{"pool", "bindings", "pool-a", "-o", "json"})
	if err := command.Execute(); err != nil {
		t.Fatalf("structured pool bindings error = %v", err)
	}
	bindings := &resourcepoolv1alpha1.ResourcePoolBindingList{}
	if err := json.Unmarshal(output.Bytes(), bindings); err != nil {
		t.Fatalf("decode pool bindings JSON: %v", err)
	}
	if len(bindings.Items) != 2 {
		t.Fatalf("pool bindings = %d, want 2", len(bindings.Items))
	}
	for i, namespace := range []string{"team-a", "team-b"} {
		if bindings.Items[i].Namespace != namespace || bindings.Items[i].Spec.PoolRef.Name != "pool-a" {
			t.Errorf("pool binding %d = %s/%s -> %s, want namespace %s and pool-a", i, bindings.Items[i].Namespace, bindings.Items[i].Name, bindings.Items[i].Spec.PoolRef.Name, namespace)
		}
	}

	options, output = newOptions()
	command = newRootCommand(options)
	command.SetArgs([]string{"pool", "bindings", "-A"})
	if err := command.Execute(); err != nil {
		t.Fatalf("all-pools bindings table error = %v", err)
	}
	wantAllTable := "" +
		"NAMESPACE  BINDING  POOL    BOUND\n" +
		"team-a     pool-a   pool-a  True\n" +
		"team-b     pool-a   pool-a  False\n" +
		"team-c     pool-b   pool-b  Unknown\n"
	if got := output.String(); got != wantAllTable {
		t.Fatalf("all-pools bindings table = %q, want %q", got, wantAllTable)
	}
}

func TestPoolBindingsRequiresNameOrAllPools(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing selection",
			args: []string{"pool", "bindings"},
			want: "either NAME or --all-pools must be specified",
		},
		{
			name: "conflicting selection",
			args: []string{"pool", "bindings", "pool-a", "-A"},
			want: "NAME cannot be combined with --all-pools",
		},
		{
			name: "target pool with namespace",
			args: []string{"pool", "bindings", "pool-a", "--namespace", "team-a"},
			want: "--namespace cannot be used with pool bindings",
		},
		{
			name: "all pools with namespace",
			args: []string{"pool", "bindings", "-A", "-n", "team-a"},
			want: "--namespace cannot be used with pool bindings",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := newRootCommand(&commandOptions{
				client: fake.NewClientBuilder().Build(),
				out:    &bytes.Buffer{},
				errOut: &bytes.Buffer{},
			})
			command.SetArgs(test.args)
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pool bindings error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPoolBindAndUnbindReplaceRootBindingCommands(t *testing.T) {
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
	command.SetArgs([]string{"pool", "bind", "training-a", "--namespace", "team-a"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool bind error = %v", err)
	}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{}
	key := client.ObjectKey{Name: "training-a", Namespace: "team-a"}
	if err := cli.Get(t.Context(), key, binding); err != nil {
		t.Fatalf("get created binding: %v", err)
	}

	command = newRootCommand(newOptions())
	command.SetArgs([]string{"pool", "unbind", "training-a", "--namespace", "team-a"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool unbind error = %v", err)
	}
	if err := cli.Get(t.Context(), key, binding); !apierrors.IsNotFound(err) {
		t.Fatalf("get deleted binding error = %v, want NotFound", err)
	}

	for _, args := range [][]string{
		{"binding", "list"},
		{"bind", "--pool", "training-a"},
		{"unbind", "--pool", "training-a"},
	} {
		command = newRootCommand(newOptions())
		command.SetArgs(args)
		if err := command.Execute(); err == nil {
			t.Errorf("removed root route %v succeeded, want rejected", args)
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

func TestPoolListTableShowsQuotaLimits(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cpu := apiresource.MustParse("128")
	memory := apiresource.MustParse("512Gi")
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&resourcepoolv1alpha1.ResourcePool{
				ObjectMeta: metav1.ObjectMeta{Name: "training-a"},
				Spec:       resourcepoolv1alpha1.ResourcePoolSpec{Quota: &resourcepoolv1alpha1.ResourcePoolQuotaSpec{CPU: &cpu, Memory: &memory}},
			},
			&resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "unlimited"}},
		).Build(),
		out: output, errOut: output,
	})
	command.SetArgs([]string{"pool", "list"})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool list error = %v", err)
	}
	for _, value := range []string{"CPU-QUOTA", "MEMORY-QUOTA", "training-a", "128", "512Gi", "unlimited", "<unlimited>"} {
		if !strings.Contains(output.String(), value) {
			t.Errorf("pool list output = %q, want %q", output.String(), value)
		}
	}
}

func TestPoolUpdateChangesOnlyRequestedSchedulingPolicies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantNode resourcepoolv1alpha1.NodeSchedulerPolicy
		wantGPU  resourcepoolv1alpha1.GPUSchedulerPolicy
	}{
		{
			name:     "node policy",
			args:     []string{"--node-scheduler-policy", "spread"},
			wantNode: resourcepoolv1alpha1.NodeSchedulerPolicySpread,
			wantGPU:  resourcepoolv1alpha1.GPUSchedulerPolicyNUMA,
		},
		{
			name:     "GPU policies",
			args:     []string{"--gpu-scheduler-policy", "topology-aware,mutex"},
			wantNode: resourcepoolv1alpha1.NodeSchedulerPolicyBinpack,
			wantGPU:  resourcepoolv1alpha1.GPUSchedulerPolicy("topology-aware,mutex"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			pool := &resourcepoolv1alpha1.ResourcePool{
				ObjectMeta: metav1.ObjectMeta{Name: "training-a", Labels: map[string]string{"owner": "platform"}},
				Spec: resourcepoolv1alpha1.ResourcePoolSpec{
					WorkloadDefaults: &resourcepoolv1alpha1.ResourcePoolWorkloadDefaults{
						SchedulingPolicy: &resourcepoolv1alpha1.ResourcePoolSchedulingPolicy{
							Node: resourcepoolv1alpha1.NodeSchedulerPolicyBinpack,
							GPU:  resourcepoolv1alpha1.GPUSchedulerPolicyNUMA,
						},
					},
				},
			}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build()
			output := &bytes.Buffer{}
			command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
			command.SetArgs(append([]string{"pool", "update", pool.Name}, test.args...))
			if err := command.Execute(); err != nil {
				t.Fatalf("pool update error = %v", err)
			}

			updated := &resourcepoolv1alpha1.ResourcePool{}
			if err := cli.Get(t.Context(), client.ObjectKey{Name: pool.Name}, updated); err != nil {
				t.Fatalf("get updated pool: %v", err)
			}
			policy := updated.Spec.WorkloadDefaults.SchedulingPolicy
			if policy.Node != test.wantNode || policy.GPU != test.wantGPU {
				t.Fatalf("updated policy = %#v, want node %q and GPU %q", policy, test.wantNode, test.wantGPU)
			}
			if updated.Labels["owner"] != "platform" {
				t.Fatalf("updated labels = %#v, want unrelated label preserved", updated.Labels)
			}
			if got, want := output.String(), "resourcepool.resource.hami.io/training-a updated\n"; got != want {
				t.Fatalf("pool update output = %q, want %q", got, want)
			}
		})
	}
}

func TestPoolUpdateValidatesSchedulingPoliciesBeforeReadingPool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no policy flags",
			args: []string{"pool", "update", "training-a"},
			want: "at least one scheduling-policy or quota flag must be specified",
		},
		{
			name: "invalid node policy",
			args: []string{"pool", "update", "training-a", "--node-scheduler-policy", "random"},
			want: `invalid node scheduler policy "random"`,
		},
		{
			name: "invalid GPU policy",
			args: []string{"pool", "update", "training-a", "--gpu-scheduler-policy", "topology-aware,exclusive"},
			want: `invalid GPU scheduler policy "exclusive"`,
		},
		{
			name: "invalid CPU quota",
			args: []string{"pool", "update", "training-a", "--cpu-quota", "lots"},
			want: `invalid CPU quota "lots"`,
		},
		{
			name: "negative memory quota",
			args: []string{"pool", "update", "training-a", "--memory-quota", "-1Gi"},
			want: "memory quota must not be negative",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := newRootCommand(&commandOptions{
				client: fake.NewClientBuilder().Build(),
				out:    &bytes.Buffer{},
				errOut: &bytes.Buffer{},
			})
			command.SetArgs(test.args)
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pool update error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPoolUpdateChangesOnlyRequestedQuotas(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cpu := apiresource.MustParse("8")
	memory := apiresource.MustParse("32Gi")
	pool := &resourcepoolv1alpha1.ResourcePool{
		ObjectMeta: metav1.ObjectMeta{Name: "training-a"},
		Spec: resourcepoolv1alpha1.ResourcePoolSpec{
			Quota: &resourcepoolv1alpha1.ResourcePoolQuotaSpec{CPU: &cpu, Memory: &memory},
			WorkloadDefaults: &resourcepoolv1alpha1.ResourcePoolWorkloadDefaults{
				SchedulingPolicy: &resourcepoolv1alpha1.ResourcePoolSchedulingPolicy{Node: resourcepoolv1alpha1.NodeSchedulerPolicySpread},
			},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build()
	runUpdate := func(args ...string) {
		t.Helper()
		command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
		command.SetArgs(append([]string{"pool", "update", pool.Name}, args...))
		if err := command.Execute(); err != nil {
			t.Fatalf("pool update %v error = %v", args, err)
		}
	}
	readPool := func() *resourcepoolv1alpha1.ResourcePool {
		t.Helper()
		updated := &resourcepoolv1alpha1.ResourcePool{}
		if err := cli.Get(t.Context(), client.ObjectKey{Name: pool.Name}, updated); err != nil {
			t.Fatalf("get updated pool: %v", err)
		}
		return updated
	}

	runUpdate("--cpu-quota", "16")
	updated := readPool()
	if updated.Spec.Quota == nil || updated.Spec.Quota.CPU == nil || updated.Spec.Quota.CPU.Cmp(apiresource.MustParse("16")) != 0 {
		t.Fatalf("updated CPU quota = %#v, want 16", updated.Spec.Quota)
	}
	if updated.Spec.Quota.Memory == nil || updated.Spec.Quota.Memory.Cmp(memory) != 0 {
		t.Fatalf("updated memory quota = %#v, want preserved 32Gi", updated.Spec.Quota)
	}
	if updated.Spec.WorkloadDefaults == nil || updated.Spec.WorkloadDefaults.SchedulingPolicy.Node != resourcepoolv1alpha1.NodeSchedulerPolicySpread {
		t.Fatalf("updated workload defaults = %#v, want preserved node policy", updated.Spec.WorkloadDefaults)
	}

	runUpdate("--memory-quota", "unlimited")
	updated = readPool()
	if updated.Spec.Quota == nil || updated.Spec.Quota.CPU == nil || updated.Spec.Quota.Memory != nil {
		t.Fatalf("updated quota = %#v, want CPU preserved and memory unlimited", updated.Spec.Quota)
	}

	runUpdate("--cpu-quota", "unlimited")
	updated = readPool()
	if updated.Spec.Quota != nil {
		t.Fatalf("updated quota = %#v, want all quotas removed", updated.Spec.Quota)
	}
}

func TestPoolUpdateRejectsConcurrentResourcePoolChange(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{
		ObjectMeta: metav1.ObjectMeta{Name: "training-a", ResourceVersion: "7"},
		Spec: resourcepoolv1alpha1.ResourcePoolSpec{
			WorkloadDefaults: &resourcepoolv1alpha1.ResourcePoolWorkloadDefaults{
				SchedulingPolicy: &resourcepoolv1alpha1.ResourcePoolSchedulingPolicy{
					Node: resourcepoolv1alpha1.NodeSchedulerPolicyBinpack,
					GPU:  resourcepoolv1alpha1.GPUSchedulerPolicyNUMA,
				},
			},
		},
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(ctx context.Context, _ client.WithWatch, object client.Object, patch client.Patch, _ ...client.PatchOption) error {
			data, err := patch.Data(object)
			if err != nil {
				t.Fatalf("encode optimistic patch: %v", err)
			}
			optimistic := make(map[string]any)
			if err := sigsyaml.Unmarshal(data, &optimistic); err != nil {
				t.Fatalf("decode optimistic patch: %v", err)
			}
			resourceVersion, found, err := unstructured.NestedString(optimistic, "metadata", "resourceVersion")
			if err != nil || !found || resourceVersion != "7" {
				t.Fatalf("optimistic patch resourceVersion = %q, found = %v, error = %v", resourceVersion, found, err)
			}
			nodePolicy, found, err := unstructured.NestedString(optimistic, "spec", "workloadDefaults", "schedulingPolicy", "node")
			if err != nil || !found || nodePolicy != "spread" {
				t.Fatalf("optimistic patch node policy = %q, found = %v, error = %v", nodePolicy, found, err)
			}
			if gpuPolicy, found, err := unstructured.NestedString(optimistic, "spec", "workloadDefaults", "schedulingPolicy", "gpu"); err != nil || found {
				t.Fatalf("optimistic patch GPU policy = %q, found = %v, error = %v; want omitted", gpuPolicy, found, err)
			}
			if _, found, err := unstructured.NestedMap(optimistic, "metadata", "labels"); err != nil || found {
				t.Fatalf("optimistic patch labels found = %v, error = %v; want omitted", found, err)
			}

			latest := pool.DeepCopy()
			latest.Labels = map[string]string{"concurrent": "preserved"}
			latest.Spec.WorkloadDefaults.SchedulingPolicy.GPU = resourcepoolv1alpha1.GPUSchedulerPolicyTopologyAware
			if err := baseClient.Update(ctx, latest); err != nil {
				t.Fatalf("simulate concurrent pool update: %v", err)
			}
			return apierrors.NewConflict(
				resourcepoolv1alpha1.GroupVersion.WithResource("resourcepools").GroupResource(),
				object.GetName(),
				fmt.Errorf("changed concurrently"),
			)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "update", pool.Name, "--node-scheduler-policy", "spread"})
	err := command.Execute()
	if err == nil || !apierrors.IsConflict(err) || !strings.Contains(err.Error(), `update resource pool "training-a"`) {
		t.Fatalf("pool update error = %v, want wrapped conflict", err)
	}
	updated := &resourcepoolv1alpha1.ResourcePool{}
	if err := baseClient.Get(t.Context(), client.ObjectKey{Name: pool.Name}, updated); err != nil {
		t.Fatalf("get concurrently updated pool: %v", err)
	}
	if updated.Labels["concurrent"] != "preserved" || updated.Spec.WorkloadDefaults.SchedulingPolicy.GPU != resourcepoolv1alpha1.GPUSchedulerPolicyTopologyAware {
		t.Fatalf("concurrent pool state = %#v, want label and GPU policy preserved", updated)
	}
}

func TestPoolDescribeShowsOnlyAssociatedNamespacesAndDevices(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cpuQuota := apiresource.MustParse("128")
	memoryQuota := apiresource.MustParse("512Gi")
	pool := &resourcepoolv1alpha1.ResourcePool{
		ObjectMeta: metav1.ObjectMeta{Name: "training-a", Generation: 7},
		Spec: resourcepoolv1alpha1.ResourcePoolSpec{
			Quota: &resourcepoolv1alpha1.ResourcePoolQuotaSpec{CPU: &cpuQuota, Memory: &memoryQuota},
			WorkloadDefaults: &resourcepoolv1alpha1.ResourcePoolWorkloadDefaults{
				SchedulingPolicy: &resourcepoolv1alpha1.ResourcePoolSchedulingPolicy{
					Node: resourcepoolv1alpha1.NodeSchedulerPolicySpread,
					GPU:  resourcepoolv1alpha1.GPUSchedulerPolicy("binpack,numa"),
				},
			},
		},
		Status: resourcepoolv1alpha1.ResourcePoolStatus{
			DiscoveredDevices: 2,
			HealthyDevices:    1,
			Revision:          "inventory-7",
			Quota: &resourcepoolv1alpha1.ResourcePoolQuotaStatus{
				ObservedGeneration: 7,
				Used: resourcepoolv1alpha1.ResourcePoolResourceUsage{
					CPU: apiresource.MustParse("32"), Memory: apiresource.MustParse("96Gi"),
				},
				Reserved: resourcepoolv1alpha1.ResourcePoolResourceUsage{
					CPU: apiresource.MustParse("4"), Memory: apiresource.MustParse("8Gi"),
				},
			},
		},
	}
	objects := []client.Object{
		pool,
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "training-a", Namespace: "team-b"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
			Status: resourcepoolv1alpha1.ResourcePoolBindingStatus{Conditions: []metav1.Condition{
				{Type: "Bound", Status: metav1.ConditionFalse},
			}},
		},
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "training-a", Namespace: "team-a"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
			Status: resourcepoolv1alpha1.ResourcePoolBindingStatus{Conditions: []metav1.Condition{
				{Type: "Bound", Status: metav1.ConditionTrue},
			}},
		},
		&resourcepoolv1alpha1.ResourcePoolBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "unrelated-namespace"},
			Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: "other"}},
		},
		&resourcepoolv1alpha1.GPUDevice{
			ObjectMeta: metav1.ObjectMeta{Name: "device-b"},
			Spec: resourcepoolv1alpha1.GPUDeviceSpec{
				Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-b", DeviceID: "GPU-B"},
				PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
			},
			Status: resourcepoolv1alpha1.GPUDeviceStatus{Discovered: true, Healthy: false},
		},
		&resourcepoolv1alpha1.GPUDevice{
			ObjectMeta: metav1.ObjectMeta{Name: "device-a"},
			Spec: resourcepoolv1alpha1.GPUDeviceSpec{
				Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceID: "GPU-A"},
				PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
			},
			Status: resourcepoolv1alpha1.GPUDeviceStatus{Discovered: true, Healthy: true},
		},
		&resourcepoolv1alpha1.GPUDevice{
			ObjectMeta: metav1.ObjectMeta{Name: "unrelated-device"},
			Spec: resourcepoolv1alpha1.GPUDeviceSpec{
				Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-c", DeviceID: "GPU-C"},
				PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: "other"},
			},
		},
	}

	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
		out:    output,
		errOut: output,
	})
	command.SetArgs([]string{"pool", "describe", pool.Name})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool describe error = %v", err)
	}

	want := "" +
		"Name:                   training-a\n" +
		"CPU quota:              128\n" +
		"Memory quota:           512Gi\n" +
		"Quota ledger:           current (generation 7)\n" +
		"CPU used:               32\n" +
		"CPU reserved:           4\n" +
		"Memory used:            96Gi\n" +
		"Memory reserved:        8Gi\n" +
		"Node scheduler policy:  spread\n" +
		"GPU scheduler policy:   binpack,numa\n" +
		"Discovered devices:     2\n" +
		"Healthy devices:        1\n" +
		"Revision:               inventory-7\n" +
		"\n" +
		"Namespaces:\n" +
		"  NAMESPACE  BINDING     BOUND\n" +
		"  team-a     training-a  True\n" +
		"  team-b     training-a  False\n" +
		"\n" +
		"Devices:\n" +
		"  NAME      NODE    DEVICE-ID  DISCOVERED  HEALTHY\n" +
		"  device-a  node-a  GPU-A      true        true\n" +
		"  device-b  node-b  GPU-B      true        false\n"
	if got := output.String(); got != want {
		t.Fatalf("pool describe output = %q, want %q", got, want)
	}
}

func TestPoolDescribeShowsEmptyAssociations(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "empty"}}
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
		out:    output,
		errOut: output,
	})
	command.SetArgs([]string{"pool", "describe", pool.Name})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool describe error = %v", err)
	}
	if !strings.Contains(output.String(), "CPU quota:              <unlimited>\nMemory quota:           <unlimited>\n") {
		t.Fatalf("pool describe empty quota output = %q, want unlimited CPU and memory", output.String())
	}
	if strings.Contains(output.String(), "Quota ledger:") {
		t.Fatalf("pool describe unconfigured quota output = %q, want no quota ledger state", output.String())
	}
	if got, want := output.String(), "Namespaces:\n  <none>\n\nDevices:\n  <none>\n"; !strings.Contains(got, want) {
		t.Fatalf("pool describe empty associations = %q, want suffix %q", got, want)
	}

	cpuQuota := apiresource.MustParse("4")
	pool.Generation = 4
	pool.Spec.Quota = &resourcepoolv1alpha1.ResourcePoolQuotaSpec{CPU: &cpuQuota}
	pool.Status.Quota = &resourcepoolv1alpha1.ResourcePoolQuotaStatus{ObservedGeneration: 3}
	output.Reset()
	command = newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
		out:    output,
		errOut: output,
	})
	command.SetArgs([]string{"pool", "describe", pool.Name})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool describe stale quota ledger error = %v", err)
	}
	if !strings.Contains(output.String(), "Quota ledger:           stale (observed generation 3, current generation 4)\n") {
		t.Fatalf("pool describe stale quota ledger output = %q", output.String())
	}

	pool.Status.Quota = nil
	output.Reset()
	command = newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
		out:    output,
		errOut: output,
	})
	command.SetArgs([]string{"pool", "describe", pool.Name})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool describe pending quota ledger error = %v", err)
	}
	if !strings.Contains(output.String(), "Quota ledger:           pending (current generation 4)\n") {
		t.Fatalf("pool describe pending quota ledger output = %q", output.String())
	}

	pool.Generation = 5
	pool.Spec.Quota = nil
	pool.Status.Quota = &resourcepoolv1alpha1.ResourcePoolQuotaStatus{
		ObservedGeneration: 4,
		Used: resourcepoolv1alpha1.ResourcePoolResourceUsage{
			CPU: apiresource.MustParse("2"),
		},
	}
	output.Reset()
	command = newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
		out:    output,
		errOut: output,
	})
	command.SetArgs([]string{"pool", "describe", pool.Name})
	if err := command.Execute(); err != nil {
		t.Fatalf("pool describe removed quota with stale ledger error = %v", err)
	}
	if !strings.Contains(output.String(), "Quota ledger:           stale (observed generation 4, current generation 5)\n") {
		t.Fatalf("pool describe removed quota with stale ledger output = %q", output.String())
	}
	if !strings.Contains(output.String(), "CPU used:               2\n") {
		t.Fatalf("pool describe removed quota usage output = %q", output.String())
	}
}

func TestPoolDeleteForceRemovesOnlyTargetPoolReferences(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	targetPool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("share-gpu-uid")}}
	otherPool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "other-pool"}}
	targetDeviceA := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1", Labels: map[string]string{"keep": "me"}},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: targetPool.Name},
		},
	}
	targetDeviceA.Labels = resourcepoolv1alpha1.PoolLabels(targetDeviceA)
	targetDeviceB := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-b", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-b", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-B"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: targetPool.Name},
		},
	}
	targetDeviceB.Labels = resourcepoolv1alpha1.PoolLabels(targetDeviceB)
	otherDevice := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-c", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-c", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-C"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: otherPool.Name},
		},
	}
	otherDevice.Labels = resourcepoolv1alpha1.PoolLabels(otherDevice)
	targetBindingA := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: targetPool.Name, Namespace: "team-a", UID: types.UID("binding-a-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: targetPool.Name}},
	}
	targetBindingB := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: targetPool.Name, Namespace: "cert-manager", UID: types.UID("binding-b-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: targetPool.Name}},
	}
	otherBinding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: otherPool.Name, Namespace: "team-a"},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: otherPool.Name}},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		targetPool,
		otherPool,
		targetDeviceA,
		targetDeviceB,
		otherDevice,
		targetBindingA,
		targetBindingB,
		otherBinding,
	).Build()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"pool", "delete", targetPool.Name, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced pool delete error = %v, output = %q", err, output.String())
	}

	if err := cli.Get(t.Context(), client.ObjectKey{Name: targetPool.Name}, &resourcepoolv1alpha1.ResourcePool{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get deleted pool error = %v, want NotFound", err)
	}
	for _, binding := range []*resourcepoolv1alpha1.ResourcePoolBinding{targetBindingA, targetBindingB} {
		key := client.ObjectKeyFromObject(binding)
		if err := cli.Get(t.Context(), key, &resourcepoolv1alpha1.ResourcePoolBinding{}); !apierrors.IsNotFound(err) {
			t.Errorf("get deleted binding %s error = %v, want NotFound", key, err)
		}
	}
	for _, device := range []*resourcepoolv1alpha1.GPUDevice{targetDeviceA, targetDeviceB} {
		stored := &resourcepoolv1alpha1.GPUDevice{}
		if err := cli.Get(t.Context(), client.ObjectKeyFromObject(device), stored); err != nil {
			t.Fatalf("get unassigned device %s: %v", device.Name, err)
		}
		if stored.Spec.PoolRef != nil {
			t.Errorf("device %s pool = %#v, want unassigned", device.Name, stored.Spec.PoolRef)
		}
		if _, found := stored.Labels[resourcepoolv1alpha1.PoolLabel]; found {
			t.Errorf("device %s retained pool label: %#v", device.Name, stored.Labels)
		}
	}
	storedDeviceA := &resourcepoolv1alpha1.GPUDevice{}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(targetDeviceA), storedDeviceA); err != nil {
		t.Fatal(err)
	}
	if storedDeviceA.Labels["keep"] != "me" {
		t.Errorf("unassign removed unrelated label: %#v", storedDeviceA.Labels)
	}
	storedOtherDevice := &resourcepoolv1alpha1.GPUDevice{}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(otherDevice), storedOtherDevice); err != nil {
		t.Fatal(err)
	}
	if got := gpuDevicePoolName(storedOtherDevice); got != otherPool.Name {
		t.Errorf("other device pool = %q, want %q", got, otherPool.Name)
	}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(otherBinding), &resourcepoolv1alpha1.ResourcePoolBinding{}); err != nil {
		t.Errorf("other binding changed: %v", err)
	}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(otherPool), &resourcepoolv1alpha1.ResourcePool{}); err != nil {
		t.Errorf("other pool changed: %v", err)
	}

	wantOutput := "resourcepoolbinding.resource.hami.io/share-gpu deleted from namespace cert-manager\n" +
		"resourcepoolbinding.resource.hami.io/share-gpu deleted from namespace team-a\n" +
		"gpudevice.resource.hami.io/device-a unassigned\n" +
		"gpudevice.resource.hami.io/device-b unassigned\n" +
		"resourcepool.resource.hami.io/share-gpu deleted\n"
	if output.String() != wantOutput {
		t.Errorf("forced pool delete output = %q, want %q", output.String(), wantOutput)
	}
}

func TestPoolDeleteForceAcceptsPoolDisappearingBeforeFinalDelete(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager", UID: types.UID("binding-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Delete: func(ctx context.Context, delegated client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			if _, ok := object.(*resourcepoolv1alpha1.ResourcePool); !ok {
				return delegated.Delete(ctx, object, opts...)
			}
			if err := delegated.Delete(ctx, object, opts...); err != nil {
				return err
			}
			return apierrors.NewNotFound(
				schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "resourcepools"},
				object.GetName(),
			)
		},
	})
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced pool delete error = %v, output = %q", err, output.String())
	}

	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get concurrently deleted pool error = %v, want NotFound", err)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(binding), &resourcepoolv1alpha1.ResourcePoolBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get deleted binding error = %v, want NotFound", err)
	}
}

func TestPoolDeleteForceRejectsPoolDisappearanceWithReferences(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager", UID: types.UID("binding-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding).Build()
	poolGets := 0
	deleteCalls := 0
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, delegated client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			if _, ok := object.(*resourcepoolv1alpha1.ResourcePool); ok {
				poolGets++
				if poolGets == 2 {
					return apierrors.NewNotFound(
						schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "resourcepools"},
						key.Name,
					)
				}
			}
			return delegated.Get(ctx, key, object, opts...)
		},
		Delete: func(ctx context.Context, delegated client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			deleteCalls++
			return delegated.Delete(ctx, object, opts...)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "re-read ResourcePool") || !apierrors.IsNotFound(err) {
		t.Fatalf("forced pool delete error = %v, want re-read NotFound", err)
	}
	if deleteCalls != 0 {
		t.Errorf("delete calls = %d, want 0", deleteCalls)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(binding), &resourcepoolv1alpha1.ResourcePoolBinding{}); err != nil {
		t.Errorf("binding changed after contradictory pool disappearance: %v", err)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); err != nil {
		t.Errorf("pool changed after contradictory pool disappearance: %v", err)
	}
}

func TestPoolDeleteForceAcceptsListedDeviceDisappearing(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	device.Labels = resourcepoolv1alpha1.PoolLabels(device)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, device).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(ctx context.Context, delegated client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := object.(*resourcepoolv1alpha1.GPUDevice); !ok {
				return delegated.Patch(ctx, object, patch, opts...)
			}
			if err := delegated.Delete(ctx, object); err != nil {
				return err
			}
			return apierrors.NewNotFound(
				schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "gpudevices"},
				object.GetName(),
			)
		},
	})
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced pool delete error = %v, output = %q", err, output.String())
	}
	if !strings.Contains(output.String(), "gpudevice.resource.hami.io/device-a already absent") {
		t.Errorf("forced pool delete output = %q, want already-absent device status", output.String())
	}

	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(device), &resourcepoolv1alpha1.GPUDevice{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get concurrently deleted device error = %v, want NotFound", err)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get deleted pool error = %v, want NotFound", err)
	}
}

func TestPoolDeleteForceStopsOnDeviceConflict(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	device.Labels = resourcepoolv1alpha1.PoolLabels(device)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, device).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(_ context.Context, _ client.WithWatch, object client.Object, _ client.Patch, _ ...client.PatchOption) error {
			return apierrors.NewConflict(
				schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "gpudevices"},
				object.GetName(),
				fmt.Errorf("concurrent assignment"),
			)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	err := command.Execute()
	if err == nil || !apierrors.IsConflict(err) || !strings.Contains(err.Error(), `unassign GPUDevice "device-a"`) {
		t.Fatalf("forced pool delete error = %v, want GPUDevice conflict", err)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); err != nil {
		t.Errorf("pool changed after device conflict: %v", err)
	}
	storedDevice := &resourcepoolv1alpha1.GPUDevice{}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(device), storedDevice); err != nil {
		t.Fatal(err)
	}
	if got := gpuDevicePoolName(storedDevice); got != pool.Name {
		t.Errorf("device pool = %q after conflict, want %q", got, pool.Name)
	}
}

func TestPoolDeleteForceCleansReferenceAddedDuringCleanup(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	deviceA := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	deviceA.Labels = resourcepoolv1alpha1.PoolLabels(deviceA)
	deviceB := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-b"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-b", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-B"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	deviceB.Labels = resourcepoolv1alpha1.PoolLabels(deviceB)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, deviceA).Build()
	deviceLists := 0
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		List: func(ctx context.Context, delegated client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*resourcepoolv1alpha1.GPUDeviceList); ok {
				deviceLists++
				if deviceLists == 2 {
					if err := delegated.Create(ctx, deviceB.DeepCopy()); err != nil {
						t.Fatalf("create late GPUDevice: %v", err)
					}
				}
			}
			return delegated.List(ctx, list, opts...)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced pool delete error = %v", err)
	}

	if deviceLists < 3 {
		t.Fatalf("GPUDevice list calls = %d, want at least 3", deviceLists)
	}
	for _, device := range []*resourcepoolv1alpha1.GPUDevice{deviceA, deviceB} {
		stored := &resourcepoolv1alpha1.GPUDevice{}
		if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(device), stored); err != nil {
			t.Fatalf("get GPUDevice %s: %v", device.Name, err)
		}
		if stored.Spec.PoolRef != nil {
			t.Errorf("GPUDevice %s still references pool: %#v", device.Name, stored.Spec.PoolRef)
		}
	}
}

func TestPoolDeleteForceFailsWhenReferencesDoNotStabilize(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build()
	deviceLists := 0
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		List: func(ctx context.Context, delegated client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*resourcepoolv1alpha1.GPUDeviceList); ok {
				deviceLists++
				device := &resourcepoolv1alpha1.GPUDevice{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("device-%d", deviceLists)},
					Spec: resourcepoolv1alpha1.GPUDeviceSpec{
						Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: fmt.Sprintf("GPU-%d", deviceLists)},
						PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
					},
				}
				device.Labels = resourcepoolv1alpha1.PoolLabels(device)
				if err := delegated.Create(ctx, device); err != nil {
					t.Fatalf("create concurrent GPUDevice: %v", err)
				}
			}
			return delegated.List(ctx, list, opts...)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "references did not stabilize after 5 cleanup passes") {
		t.Fatalf("forced pool delete error = %v, want bounded cleanup failure", err)
	}
	if deviceLists != maxPoolReferenceCleanupPasses+1 {
		t.Errorf("GPUDevice list calls = %d, want %d", deviceLists, maxPoolReferenceCleanupPasses+1)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); err != nil {
		t.Errorf("pool changed after unstable cleanup: %v", err)
	}
}

func TestPoolDeleteWithoutForceDoesNotRemoveReferences(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu"}}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager"},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	device.Labels = resourcepoolv1alpha1.PoolLabels(device)
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding, device).Build()
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name})
	if err := command.Execute(); err != nil {
		t.Fatalf("plain pool delete error = %v", err)
	}

	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(binding), &resourcepoolv1alpha1.ResourcePoolBinding{}); err != nil {
		t.Errorf("plain delete changed binding: %v", err)
	}
	storedDevice := &resourcepoolv1alpha1.GPUDevice{}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(device), storedDevice); err != nil {
		t.Fatal(err)
	}
	if got := gpuDevicePoolName(storedDevice); got != pool.Name {
		t.Errorf("device pool = %q after plain delete, want %q", got, pool.Name)
	}
}

func TestPoolDeleteWithoutForceReportsNotFound(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand(&commandOptions{
		client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		out:    &bytes.Buffer{},
		errOut: &bytes.Buffer{},
	})
	command.SetArgs([]string{"pool", "delete", "missing-pool"})
	err := command.Execute()
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("plain pool delete error = %v, want NotFound", err)
	}
}

func TestPoolDeleteForceStopsWhenReferenceCleanupFails(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager", UID: types.UID("binding-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	device.Labels = resourcepoolv1alpha1.PoolLabels(device)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding, device).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Delete: func(ctx context.Context, delegated client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			if _, ok := object.(*resourcepoolv1alpha1.ResourcePoolBinding); ok {
				return apierrors.NewForbidden(
					schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "resourcepoolbindings"},
					object.GetName(),
					fmt.Errorf("test denial"),
				)
			}
			return delegated.Delete(ctx, object, opts...)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "delete ResourcePoolBinding cert-manager/share-gpu") {
		t.Fatalf("forced pool delete error = %v, want binding cleanup failure", err)
	}

	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); err != nil {
		t.Errorf("pool changed after cleanup failure: %v", err)
	}
	storedDevice := &resourcepoolv1alpha1.GPUDevice{}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(device), storedDevice); err != nil {
		t.Fatal(err)
	}
	if got := gpuDevicePoolName(storedDevice); got != pool.Name {
		t.Errorf("device pool = %q after cleanup failure, want %q", got, pool.Name)
	}
}

func TestPoolDeleteForcePreflightsAllReferenceReads(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("pool-uid")}}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager", UID: types.UID("binding-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		List: func(ctx context.Context, delegated client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*resourcepoolv1alpha1.GPUDeviceList); ok {
				return apierrors.NewForbidden(
					schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "gpudevices"},
					"",
					fmt.Errorf("test denial"),
				)
			}
			return delegated.List(ctx, list, opts...)
		},
	})
	command := newRootCommand(&commandOptions{client: cli, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "list GPUDevices") {
		t.Fatalf("forced pool delete error = %v, want device list failure", err)
	}

	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(binding), &resourcepoolv1alpha1.ResourcePoolBinding{}); err != nil {
		t.Errorf("binding changed before reference preflight completed: %v", err)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(pool), &resourcepoolv1alpha1.ResourcePool{}); err != nil {
		t.Errorf("pool changed before reference preflight completed: %v", err)
	}
}

func TestPoolDeleteForceRejectsSameNamePoolReplacementBeforeMutation(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "share-gpu", UID: types.UID("original-pool-uid")}}
	replacementUID := types.UID("replacement-pool-uid")
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager", UID: types.UID("binding-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	device.Labels = resourcepoolv1alpha1.PoolLabels(device)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding, device).Build()
	replaced := false
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		List: func(ctx context.Context, delegated client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*resourcepoolv1alpha1.ResourcePoolBindingList); ok && !replaced {
				replaced = true
				if err := delegated.Delete(ctx, pool); err != nil {
					t.Fatalf("delete original pool: %v", err)
				}
				replacement := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: pool.Name, UID: replacementUID}}
				if err := delegated.Create(ctx, replacement); err != nil {
					t.Fatalf("create replacement pool: %v", err)
				}
			}
			return delegated.List(ctx, list, opts...)
		},
	})
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), `ResourcePool UID is "replacement-pool-uid", expected "original-pool-uid"`) {
		t.Fatalf("forced pool delete error = %v, want pool UID mismatch", err)
	}
	if output.Len() != 0 {
		t.Errorf("forced pool delete wrote mutation output before UID validation: %q", output.String())
	}

	storedPool := &resourcepoolv1alpha1.ResourcePool{}
	if err := baseClient.Get(t.Context(), client.ObjectKey{Name: pool.Name}, storedPool); err != nil {
		t.Fatalf("get replacement pool: %v", err)
	}
	if storedPool.UID != replacementUID {
		t.Errorf("stored pool UID = %q, want %q", storedPool.UID, replacementUID)
	}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(binding), &resourcepoolv1alpha1.ResourcePoolBinding{}); err != nil {
		t.Errorf("replacement binding changed: %v", err)
	}
	storedDevice := &resourcepoolv1alpha1.GPUDevice{}
	if err := baseClient.Get(t.Context(), client.ObjectKeyFromObject(device), storedDevice); err != nil {
		t.Fatal(err)
	}
	if got := gpuDevicePoolName(storedDevice); got != pool.Name {
		t.Errorf("replacement device pool = %q, want %q", got, pool.Name)
	}
}

func TestPoolDeleteForceClearsReferencesFromTerminatingPool(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	deletionTime := metav1.Now()
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{
		Name:              "share-gpu",
		UID:               types.UID("pool-uid"),
		DeletionTimestamp: &deletionTime,
		Finalizers:        []string{"resource.hami.io/reference-protection"},
	}}
	binding := &resourcepoolv1alpha1.ResourcePoolBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: "cert-manager", UID: types.UID("binding-uid")},
		Spec:       resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool.Name}},
	}
	device := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", ResourceVersion: "1"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			Identity: resourcepoolv1alpha1.DeviceIdentity{NodeName: "node-a", DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: "GPU-A"},
			PoolRef:  &resourcepoolv1alpha1.PoolReference{Name: pool.Name},
		},
	}
	device.Labels = resourcepoolv1alpha1.PoolLabels(device)
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, binding, device).Build()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"pool", "delete", pool.Name, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced terminating pool delete error = %v, output = %q", err, output.String())
	}

	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(binding), &resourcepoolv1alpha1.ResourcePoolBinding{}); !apierrors.IsNotFound(err) {
		t.Errorf("get deleted binding error = %v, want NotFound", err)
	}
	storedDevice := &resourcepoolv1alpha1.GPUDevice{}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(device), storedDevice); err != nil {
		t.Fatal(err)
	}
	if storedDevice.Spec.PoolRef != nil {
		t.Errorf("terminating pool device still assigned: %#v", storedDevice.Spec.PoolRef)
	}
	storedPool := &resourcepoolv1alpha1.ResourcePool{}
	if err := cli.Get(t.Context(), client.ObjectKeyFromObject(pool), storedPool); err != nil {
		t.Fatalf("get finalizer-protected pool: %v", err)
	}
	if storedPool.DeletionTimestamp.IsZero() {
		t.Error("finalizer-protected pool lost deletion timestamp")
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

func TestDeviceUnassignExpectedOwnershipPreconditions(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		uid          string
		pool         string
		args         []string
		wantErr      string
		wantAssigned bool
	}{
		{
			name: "unguarded unassign",
			uid:  "device-uid",
			pool: "temporary-pool",
			args: []string{"device", "unassign", "device-a"},
		},
		{
			name: "matching ownership preconditions",
			uid:  "device-uid",
			pool: "temporary-pool",
			args: []string{"device", "unassign", "device-a", "--expected-uid", "device-uid", "--expected-pool", "temporary-pool"},
		},
		{
			name:         "unexpected uid",
			uid:          "replacement-uid",
			pool:         "temporary-pool",
			args:         []string{"device", "unassign", "device-a", "--expected-uid", "device-uid", "--expected-pool", "temporary-pool"},
			wantErr:      `UID is "replacement-uid", expected "device-uid"`,
			wantAssigned: true,
		},
		{
			name:         "unexpected pool",
			uid:          "device-uid",
			pool:         "production-pool",
			args:         []string{"device", "unassign", "device-a", "--expected-uid", "device-uid", "--expected-pool", "temporary-pool"},
			wantErr:      `pool is "production-pool", expected "temporary-pool"`,
			wantAssigned: true,
		},
		{
			name:         "expected uid without pool",
			uid:          "device-uid",
			pool:         "temporary-pool",
			args:         []string{"device", "unassign", "device-a", "--expected-uid", "device-uid"},
			wantErr:      "--expected-uid and --expected-pool must be used together",
			wantAssigned: true,
		},
		{
			name:         "expected pool without uid",
			uid:          "device-uid",
			pool:         "temporary-pool",
			args:         []string{"device", "unassign", "device-a", "--expected-pool", "temporary-pool"},
			wantErr:      "--expected-uid and --expected-pool must be used together",
			wantAssigned: true,
		},
		{
			name:         "empty ownership precondition",
			uid:          "device-uid",
			pool:         "temporary-pool",
			args:         []string{"device", "unassign", "device-a", "--expected-uid=", "--expected-pool", "temporary-pool"},
			wantErr:      "--expected-uid and --expected-pool must not be empty",
			wantAssigned: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := &resourcepoolv1alpha1.GPUDevice{
				ObjectMeta: metav1.ObjectMeta{Name: "device-a", UID: types.UID(test.uid), ResourceVersion: "1"},
				Spec: resourcepoolv1alpha1.GPUDeviceSpec{
					PoolRef: &resourcepoolv1alpha1.PoolReference{Name: test.pool},
				},
			}
			device.Labels = resourcepoolv1alpha1.PoolLabels(device)
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device).Build()
			output := &bytes.Buffer{}
			command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
			command.SetArgs(test.args)

			err := command.Execute()
			if test.wantErr == "" && err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("Execute() error = %v, want substring %q", err, test.wantErr)
			}

			updated := &resourcepoolv1alpha1.GPUDevice{}
			if err := cli.Get(t.Context(), client.ObjectKey{Name: device.Name}, updated); err != nil {
				t.Fatal(err)
			}
			if test.wantAssigned {
				if updated.Spec.PoolRef == nil || updated.Spec.PoolRef.Name != test.pool {
					t.Fatalf("updated pool = %#v, want unchanged %q", updated.Spec.PoolRef, test.pool)
				}
			} else if updated.Spec.PoolRef != nil {
				t.Fatalf("updated pool = %#v, want unassigned", updated.Spec.PoolRef)
			}
		})
	}
}

func TestDeviceUnassignOwnershipGuardRejectsConcurrentReassignment(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	current := &resourcepoolv1alpha1.GPUDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "device-a", UID: "device-uid", ResourceVersion: "7"},
		Spec: resourcepoolv1alpha1.GPUDeviceSpec{
			PoolRef: &resourcepoolv1alpha1.PoolReference{Name: "temporary-pool"},
		},
	}
	current.Labels = resourcepoolv1alpha1.PoolLabels(current)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(current).Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(ctx context.Context, _ client.WithWatch, object client.Object, patch client.Patch, _ ...client.PatchOption) error {
			data, err := patch.Data(object)
			if err != nil {
				t.Fatalf("encode optimistic patch: %v", err)
			}
			optimistic := make(map[string]any)
			if err := sigsyaml.Unmarshal(data, &optimistic); err != nil {
				t.Fatalf("decode optimistic patch: %v", err)
			}
			resourceVersion, found, err := unstructured.NestedString(optimistic, "metadata", "resourceVersion")
			if err != nil || !found || resourceVersion != "7" {
				t.Fatalf("optimistic patch resourceVersion = %q, found = %v, error = %v", resourceVersion, found, err)
			}

			latest := current.DeepCopy()
			latest.Spec.PoolRef = &resourcepoolv1alpha1.PoolReference{Name: "production-pool"}
			latest.Labels = resourcepoolv1alpha1.PoolLabels(latest)
			if err := baseClient.Update(ctx, latest); err != nil {
				t.Fatalf("simulate concurrent reassignment: %v", err)
			}
			return apierrors.NewConflict(schema.GroupResource{Group: resourcepoolv1alpha1.GroupVersion.Group, Resource: "gpudevices"}, object.GetName(), fmt.Errorf("concurrent update"))
		},
	})
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"device", "unassign", "device-a", "--expected-uid", "device-uid", "--expected-pool", "temporary-pool"})

	err := command.Execute()
	if !apierrors.IsConflict(err) {
		t.Fatalf("Execute() error = %v, want conflict", err)
	}
	updated := &resourcepoolv1alpha1.GPUDevice{}
	if err := baseClient.Get(t.Context(), client.ObjectKey{Name: current.Name}, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.PoolRef == nil || updated.Spec.PoolRef.Name != "production-pool" {
		t.Fatalf("updated pool = %#v, want concurrent production-pool assignment preserved", updated.Spec.PoolRef)
	}
}

func TestApplyRejectsUnknownTypedFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "GPUDevice field",
			manifest: `{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice","metadata":{"name":"device-a","resourceVersion":"7"},"spec":{"identity":{"nodeName":"node-a","deviceType":"NVIDIA","deviceID":"GPU-A"},"poolReff":{"name":"training-a"}}}`,
			want:     `unknown field "poolReff"`,
		},
		{
			name:     "ResourcePoolBinding field",
			manifest: `{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePoolBinding","metadata":{"name":"training-a","namespace":"team-a"},"spec":{"poolReff":{"name":"training-a"}}}`,
			want:     `unknown field "poolReff"`,
		},
		{
			name:     "nested pool reference field",
			manifest: `{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice","metadata":{"name":"device-a","resourceVersion":"7"},"spec":{"identity":{"nodeName":"node-a","deviceType":"NVIDIA","deviceID":"GPU-A"},"poolRef":{"name":"training-a","naem":"training-b"}}}`,
			want:     `unknown field "naem"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "object.json")
			if err := os.WriteFile(path, []byte(test.manifest), 0o600); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			output := &bytes.Buffer{}
			command := newRootCommand(&commandOptions{
				client: fake.NewClientBuilder().Build(),
				out:    output,
				errOut: output,
			})
			command.SetArgs([]string{"apply", "-f", path})
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("apply error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestApplyResourcePoolPreservesExtensionFields(t *testing.T) {
	t.Parallel()
	manifest := []byte(`{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePool","metadata":{"name":"training-a"},"spec":{"futureField":{"enabled":true},"workloadDefaults":{"schedulingPolicy":{"node":"spread","gpu":"binpack,numa"}}}}`)
	path := filepath.Join(t.TempDir(), "pool.json")
	if err := os.WriteFile(path, manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	applied := &unstructured.Unstructured{}
	patchCalled := false
	baseClient := fake.NewClientBuilder().Build()
	cli := interceptor.NewClient(baseClient, interceptor.Funcs{
		Patch: func(_ context.Context, _ client.WithWatch, patched client.Object, patch client.Patch, _ ...client.PatchOption) error {
			patchCalled = true
			data, err := patch.Data(patched)
			if err != nil {
				return err
			}
			if err := sigsyaml.Unmarshal(data, applied); err != nil {
				t.Fatalf("decode apply patch: %v", err)
			}
			return nil
		},
	})
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{client: cli, out: output, errOut: output})
	command.SetArgs([]string{"apply", "-f", path})
	if err := command.Execute(); err != nil {
		t.Fatalf("apply ResourcePool: %v", err)
	}
	if !patchCalled {
		t.Fatal("apply did not patch ResourcePool")
	}
	enabled, found, err := unstructured.NestedBool(applied.Object, "spec", "futureField", "enabled")
	if err != nil || !found || !enabled {
		t.Fatalf("future field enabled = %t, found = %t, error = %v", enabled, found, err)
	}
	nodePolicy, found, err := unstructured.NestedString(applied.Object, "spec", "workloadDefaults", "schedulingPolicy", "node")
	if err != nil || !found || nodePolicy != "spread" {
		t.Fatalf("node policy = %q, found = %v, error = %v", nodePolicy, found, err)
	}
	gpuPolicy, found, err := unstructured.NestedString(applied.Object, "spec", "workloadDefaults", "schedulingPolicy", "gpu")
	if err != nil || !found || gpuPolicy != "binpack,numa" {
		t.Fatalf("GPU policy = %q, found = %v, error = %v", gpuPolicy, found, err)
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
			if err != nil {
				t.Fatalf("encode optimistic patch: %v", err)
			}
			optimistic := make(map[string]any)
			if err := sigsyaml.Unmarshal(data, &optimistic); err != nil {
				t.Fatalf("decode optimistic patch: %v", err)
			}
			resourceVersion, found, err := unstructured.NestedString(optimistic, "metadata", "resourceVersion")
			if err != nil || !found || resourceVersion != "7" {
				t.Fatalf("optimistic patch resourceVersion = %q, found = %v, error = %v", resourceVersion, found, err)
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
