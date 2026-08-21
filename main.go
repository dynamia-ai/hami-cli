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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	resourcepoolv1alpha1 "github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api/v1alpha1"
)

type commandOptions struct {
	loadingRules *clientcmd.ClientConfigLoadingRules
	overrides    *clientcmd.ConfigOverrides
	clientConfig clientcmd.ClientConfig
	client       client.Client
	out          io.Writer
	errOut       io.Writer
	executable   string
}

func main() {
	options := &commandOptions{out: os.Stdout, errOut: os.Stderr}
	command := newRootCommand(options)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{
		Use:           "hami-cli",
		Short:         "Manage HAMi Enterprise physical GPU resource pools",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.SetOut(options.out)
	command.SetErr(options.errOut)
	if displayName := pluginDisplayName(options.executable); displayName != "" {
		command.Annotations = map[string]string{cobra.CommandDisplayNameAnnotation: displayName}
	}
	options.bindConfigFlags(command)
	command.AddCommand(
		newPoolCommand(options),
		newDeviceCommand(options),
		newBindingCommand(options),
		newApplyCommand(options),
		newVersionCommand(options),
	)
	command.InitDefaultCompletionCmd()
	return command
}

func newPoolCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{Use: "pool", Short: "Manage ResourcePool objects"}
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List resource pools",
		RunE: func(command *cobra.Command, _ []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			output, _ := command.Flags().GetString("output")
			if output != "table" {
				pools := &unstructured.UnstructuredList{}
				pools.SetGroupVersionKind(schema.FromAPIVersionAndKind(resourcepoolv1alpha1.GroupVersion.String(), "ResourcePoolList"))
				if err := cli.List(command.Context(), pools); err != nil {
					return fmt.Errorf("list resource pools: %w", err)
				}
				sort.Slice(pools.Items, func(i, j int) bool { return pools.Items[i].GetName() < pools.Items[j].GetName() })
				return printObject(options.out, output, pools, unstructuredPoolNames(pools))
			}
			pools := &resourcepoolv1alpha1.ResourcePoolList{}
			if err := cli.List(command.Context(), pools); err != nil {
				return fmt.Errorf("list resource pools: %w", err)
			}
			sort.Slice(pools.Items, func(i, j int) bool { return pools.Items[i].Name < pools.Items[j].Name })
			fmt.Fprintln(options.out, "NAME\tDISCOVERED\tHEALTHY\tREVISION")
			for i := range pools.Items {
				pool := &pools.Items[i]
				fmt.Fprintf(options.out, "%s\t%d\t%d\t%s\n", pool.Name, pool.Status.DiscoveredDevices, pool.Status.HealthyDevices, pool.Status.Revision)
			}
			return nil
		},
	}
	addOutputFlag(list)
	command.AddCommand(
		list,
		&cobra.Command{
			Use:   "create NAME",
			Short: "Create an empty resource pool",
			Args:  cobra.ExactArgs(1),
			RunE: func(command *cobra.Command, args []string) error {
				cli, err := options.getClient()
				if err != nil {
					return err
				}
				pool := &resourcepoolv1alpha1.ResourcePool{
					TypeMeta:   metav1.TypeMeta{APIVersion: resourcepoolv1alpha1.GroupVersion.String(), Kind: "ResourcePool"},
					ObjectMeta: metav1.ObjectMeta{Name: args[0]},
					Spec:       resourcepoolv1alpha1.ResourcePoolSpec{},
				}
				if err := cli.Create(command.Context(), pool); err != nil {
					return fmt.Errorf("create resource pool %q: %w", args[0], err)
				}
				fmt.Fprintf(options.out, "resourcepool.resource.hami.io/%s created\n", args[0])
				return nil
			},
		},
		&cobra.Command{
			Use:   "delete NAME",
			Short: "Delete an unreferenced resource pool",
			Args:  cobra.ExactArgs(1),
			RunE: func(command *cobra.Command, args []string) error {
				cli, err := options.getClient()
				if err != nil {
					return err
				}
				pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: args[0]}}
				if err := cli.Delete(command.Context(), pool); err != nil {
					return fmt.Errorf("delete resource pool %q: %w", args[0], err)
				}
				fmt.Fprintf(options.out, "resourcepool.resource.hami.io/%s deleted\n", args[0])
				return nil
			},
		},
	)
	return command
}

func newDeviceCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{Use: "device", Short: "Inspect and assign discovered GPUDevice objects"}
	var nodeName string
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List discovered physical GPUs",
		RunE: func(command *cobra.Command, _ []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			devices := &resourcepoolv1alpha1.GPUDeviceList{}
			if err := cli.List(command.Context(), devices); err != nil {
				return fmt.Errorf("list GPU devices: %w", err)
			}
			if nodeName != "" {
				filtered := devices.Items[:0]
				for i := range devices.Items {
					if devices.Items[i].Spec.Identity.NodeName == nodeName {
						filtered = append(filtered, devices.Items[i])
					}
				}
				devices.Items = filtered
			}
			sort.Slice(devices.Items, func(i, j int) bool {
				left, right := &devices.Items[i], &devices.Items[j]
				if left.Spec.Identity.NodeName == right.Spec.Identity.NodeName {
					return left.Spec.Identity.DeviceID < right.Spec.Identity.DeviceID
				}
				return left.Spec.Identity.NodeName < right.Spec.Identity.NodeName
			})
			output, _ := command.Flags().GetString("output")
			if output != "table" {
				devices.TypeMeta = metav1.TypeMeta{APIVersion: resourcepoolv1alpha1.GroupVersion.String(), Kind: "GPUDeviceList"}
				return printObject(options.out, output, devices, deviceNames(devices))
			}
			fmt.Fprintln(options.out, "NAME\tNODE\tDEVICE-ID\tPOOL\tDISCOVERED\tHEALTHY")
			for i := range devices.Items {
				device := &devices.Items[i]
				pool := "<unassigned>"
				if device.Spec.PoolRef != nil {
					pool = device.Spec.PoolRef.Name
				}
				fmt.Fprintf(options.out, "%s\t%s\t%s\t%s\t%t\t%t\n", device.Name, device.Spec.Identity.NodeName, device.Spec.Identity.DeviceID, pool, device.Status.Discovered, device.Status.Healthy)
			}
			return nil
		},
	}
	list.Flags().StringVar(&nodeName, "node", "", "Only show devices on this Node")
	addOutputFlag(list)

	command.AddCommand(list, newDeviceAssignCommand(options), newDeviceUnassignCommand(options))
	return command
}

func newDeviceAssignCommand(options *commandOptions) *cobra.Command {
	var nodeName, deviceID string
	command := &cobra.Command{
		Use:   "assign [DEVICE_NAME] --pool POOL",
		Short: "Atomically assign one physical GPU to one pool",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			name, err := deviceNameFromArgs(args, nodeName, deviceID)
			if err != nil {
				return err
			}
			device := &resourcepoolv1alpha1.GPUDevice{}
			if err := cli.Get(command.Context(), client.ObjectKey{Name: name}, device); err != nil {
				return fmt.Errorf("get GPUDevice %q: %w", name, err)
			}
			pool, _ := command.Flags().GetString("pool")
			base := device.DeepCopy()
			device.Spec.PoolRef = &resourcepoolv1alpha1.PoolReference{Name: pool}
			device.Labels = resourcepoolv1alpha1.PoolLabels(device)
			if err := cli.Patch(command.Context(), device, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				return fmt.Errorf("assign GPUDevice %q to resource pool %q: %w", name, pool, err)
			}
			fmt.Fprintf(options.out, "gpudevice.resource.hami.io/%s assigned to %s\n", name, pool)
			return nil
		},
	}
	command.Flags().StringVar(&nodeName, "node", "", "Kubernetes Node name")
	command.Flags().StringVar(&deviceID, "device-id", "", "Physical GPU UUID")
	command.Flags().String("pool", "", "ResourcePool name")
	_ = command.MarkFlagRequired("pool")
	return command
}

func newDeviceUnassignCommand(options *commandOptions) *cobra.Command {
	var nodeName, deviceID string
	command := &cobra.Command{
		Use:   "unassign [DEVICE_NAME]",
		Short: "Remove one physical GPU from its resource pool",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			name, err := deviceNameFromArgs(args, nodeName, deviceID)
			if err != nil {
				return err
			}
			device := &resourcepoolv1alpha1.GPUDevice{}
			if err := cli.Get(command.Context(), client.ObjectKey{Name: name}, device); err != nil {
				return fmt.Errorf("get GPUDevice %q: %w", name, err)
			}
			base := device.DeepCopy()
			device.Spec.PoolRef = nil
			device.Labels = resourcepoolv1alpha1.PoolLabels(device)
			if err := cli.Patch(command.Context(), device, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				return fmt.Errorf("unassign GPUDevice %q: %w", name, err)
			}
			fmt.Fprintf(options.out, "gpudevice.resource.hami.io/%s unassigned\n", name)
			return nil
		},
	}
	command.Flags().StringVar(&nodeName, "node", "", "Kubernetes Node name")
	command.Flags().StringVar(&deviceID, "device-id", "", "Physical GPU UUID")
	return command
}

func newBindingCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{Use: "binding", Short: "Manage namespace access to ResourcePools"}
	command.AddCommand(newBindingListCommand(options), newBindCommand(options), newUnbindCommand(options))
	return command
}

func newBindingListCommand(options *commandOptions) *cobra.Command {
	var allNamespaces bool
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List namespace resource-pool bindings",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			bindings := &resourcepoolv1alpha1.ResourcePoolBindingList{}
			listOptions := make([]client.ListOption, 0, 1)
			if !allNamespaces {
				namespace, err := options.namespace()
				if err != nil {
					return err
				}
				listOptions = append(listOptions, client.InNamespace(namespace))
			}
			if err := cli.List(command.Context(), bindings, listOptions...); err != nil {
				return fmt.Errorf("list resource-pool bindings: %w", err)
			}
			sort.Slice(bindings.Items, func(i, j int) bool {
				if bindings.Items[i].Namespace == bindings.Items[j].Namespace {
					return bindings.Items[i].Name < bindings.Items[j].Name
				}
				return bindings.Items[i].Namespace < bindings.Items[j].Namespace
			})
			output, _ := command.Flags().GetString("output")
			if output != "table" {
				bindings.TypeMeta = metav1.TypeMeta{APIVersion: resourcepoolv1alpha1.GroupVersion.String(), Kind: "ResourcePoolBindingList"}
				return printObject(options.out, output, bindings, bindingNames(bindings))
			}
			fmt.Fprintln(options.out, "NAMESPACE\tNAME\tPOOL\tBOUND")
			for i := range bindings.Items {
				binding := &bindings.Items[i]
				fmt.Fprintf(options.out, "%s\t%s\t%s\t%s\n", binding.Namespace, binding.Name, binding.Spec.PoolRef.Name, bindingConditionStatus(binding))
			}
			return nil
		},
	}
	command.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "List bindings across all namespaces")
	addOutputFlag(command)
	return command
}

func newBindCommand(options *commandOptions) *cobra.Command {
	var pool string
	command := &cobra.Command{
		Use:   "bind --namespace NAMESPACE --pool POOL",
		Short: "Grant a namespace access to a resource pool",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			namespace, err := options.namespace()
			if err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			binding := &resourcepoolv1alpha1.ResourcePoolBinding{
				TypeMeta: metav1.TypeMeta{APIVersion: resourcepoolv1alpha1.GroupVersion.String(), Kind: "ResourcePoolBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      pool,
					Namespace: namespace,
					Labels:    map[string]string{resourcepoolv1alpha1.PoolLabel: pool},
				},
				Spec: resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: pool}},
			}
			if err := cli.Create(command.Context(), binding); err != nil {
				return fmt.Errorf("bind namespace %q to resource pool %q: %w", namespace, pool, err)
			}
			fmt.Fprintf(options.out, "resourcepoolbinding.resource.hami.io/%s created in namespace %s\n", pool, namespace)
			return nil
		},
	}
	command.Flags().StringVar(&pool, "pool", "", "ResourcePool name")
	_ = command.MarkFlagRequired("pool")
	return command
}

func newUnbindCommand(options *commandOptions) *cobra.Command {
	var pool string
	command := &cobra.Command{
		Use:   "unbind --namespace NAMESPACE --pool POOL",
		Short: "Revoke a namespace's resource-pool access for future Pods",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			namespace, err := options.namespace()
			if err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			binding := &resourcepoolv1alpha1.ResourcePoolBinding{ObjectMeta: metav1.ObjectMeta{Name: pool, Namespace: namespace}}
			if err := cli.Delete(command.Context(), binding); err != nil {
				return fmt.Errorf("unbind namespace %q from resource pool %q: %w", namespace, pool, err)
			}
			fmt.Fprintf(options.out, "resourcepoolbinding.resource.hami.io/%s deleted from namespace %s\n", pool, namespace)
			return nil
		},
	}
	command.Flags().StringVar(&pool, "pool", "", "ResourcePool name")
	_ = command.MarkFlagRequired("pool")
	return command
}

func newApplyCommand(options *commandOptions) *cobra.Command {
	var fileName string
	command := &cobra.Command{
		Use:   "apply -f FILE",
		Short: "Create or replace resource-pool CRD objects from YAML",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			input := io.Reader(os.Stdin)
			if fileName != "-" {
				file, err := os.Open(fileName)
				if err != nil {
					return fmt.Errorf("open %q: %w", fileName, err)
				}
				defer file.Close()
				input = file
			}
			decoder := yaml.NewYAMLOrJSONDecoder(input, 64*1024)
			for {
				raw := &runtime.RawExtension{}
				if err := decoder.Decode(raw); err != nil {
					if errors.Is(err, io.EOF) {
						return nil
					}
					return fmt.Errorf("decode input: %w", err)
				}
				if len(strings.TrimSpace(string(raw.Raw))) == 0 {
					continue
				}
				object, err := decodeResourcePoolObject(raw.Raw)
				if err != nil {
					return err
				}
				if err := options.defaultApplyNamespace(object); err != nil {
					return err
				}
				if err := applyResourcePoolObject(command.Context(), cli, object); err != nil {
					return fmt.Errorf("apply %s/%s: %w", object.GetObjectKind().GroupVersionKind().Kind, object.GetName(), err)
				}
				fmt.Fprintf(options.out, "%s/%s applied\n", strings.ToLower(object.GetObjectKind().GroupVersionKind().Kind), object.GetName())
			}
		},
	}
	command.Flags().StringVarP(&fileName, "filename", "f", "", "YAML or JSON file; use - for stdin")
	_ = command.MarkFlagRequired("filename")
	return command
}

func applyResourcePoolObject(ctx context.Context, cli client.Client, object client.Object) error {
	requestedDevice, isGPUDevice := object.(*resourcepoolv1alpha1.GPUDevice)
	if !isGPUDevice {
		patch, err := json.Marshal(object)
		if err != nil {
			return fmt.Errorf("encode object: %w", err)
		}
		return cli.Patch(ctx, object, client.RawPatch(types.ApplyPatchType, patch), client.FieldOwner("hami-cli"))
	}
	current := &resourcepoolv1alpha1.GPUDevice{}
	if err := cli.Get(ctx, client.ObjectKey{Name: requestedDevice.Name}, current); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("GPUDevice %q is controller-managed and cannot be created with apply", requestedDevice.Name)
		}
		return fmt.Errorf("get current GPUDevice %q: %w", requestedDevice.Name, err)
	}
	if requestedDevice.ResourceVersion == "" {
		return fmt.Errorf("existing GPUDevice manifests must include metadata.resourceVersion; export the current object with kubectl get gpudevice %s -o yaml before changing spec.poolRef", requestedDevice.Name)
	}
	if current.ResourceVersion != requestedDevice.ResourceVersion {
		return fmt.Errorf(
			"GPUDevice %q changed since resourceVersion %q; current resourceVersion is %q and current pool is %s",
			requestedDevice.Name,
			requestedDevice.ResourceVersion,
			current.ResourceVersion,
			gpuDevicePoolName(current),
		)
	}
	if current.Spec.Identity != requestedDevice.Spec.Identity {
		return fmt.Errorf("GPUDevice %q identity does not match the controller-managed inventory", requestedDevice.Name)
	}
	base := current.DeepCopy()
	current.Spec.PoolRef = requestedDevice.Spec.PoolRef.DeepCopy()
	current.Labels = resourcepoolv1alpha1.PoolLabels(current)
	if err := cli.Patch(ctx, current, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			latest := &resourcepoolv1alpha1.GPUDevice{}
			if getErr := cli.Get(ctx, client.ObjectKey{Name: requestedDevice.Name}, latest); getErr == nil {
				return fmt.Errorf("GPUDevice %q changed concurrently; current resourceVersion is %q and current pool is %s: %w", requestedDevice.Name, latest.ResourceVersion, gpuDevicePoolName(latest), err)
			}
		}
		return err
	}
	return nil
}

func gpuDevicePoolName(device *resourcepoolv1alpha1.GPUDevice) string {
	if device.Spec.PoolRef == nil {
		return "<unassigned>"
	}
	return device.Spec.PoolRef.Name
}

func (o *commandOptions) getClient() (client.Client, error) {
	if o.client != nil {
		return o.client, nil
	}
	clientConfig := o.getClientConfig()
	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes client configuration: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Kubernetes scheme: %w", err)
	}
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register resource-pool scheme: %w", err)
	}
	o.client, err = client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return o.client, nil
}

func (o *commandOptions) bindConfigFlags(command *cobra.Command) {
	if o.loadingRules == nil {
		o.loadingRules = clientcmd.NewDefaultClientConfigLoadingRules()
	}
	if o.overrides == nil {
		o.overrides = &clientcmd.ConfigOverrides{}
	}
	command.PersistentFlags().StringVar(&o.loadingRules.ExplicitPath, "kubeconfig", "", "Path to the kubeconfig file")
	clientcmd.BindOverrideFlags(o.overrides, command.PersistentFlags(), clientcmd.RecommendedConfigOverrideFlags(""))
}

func (o *commandOptions) getClientConfig() clientcmd.ClientConfig {
	if o.clientConfig != nil {
		return o.clientConfig
	}
	if o.loadingRules == nil {
		o.loadingRules = clientcmd.NewDefaultClientConfigLoadingRules()
	}
	if o.overrides == nil {
		o.overrides = &clientcmd.ConfigOverrides{}
	}
	o.clientConfig = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(o.loadingRules, o.overrides)
	return o.clientConfig
}

func (o *commandOptions) namespace() (string, error) {
	namespace, _, err := o.getClientConfig().Namespace()
	if err != nil {
		return "", fmt.Errorf("resolve Kubernetes namespace: %w", err)
	}
	return namespace, nil
}

func (o *commandOptions) defaultApplyNamespace(object client.Object) error {
	binding, ok := object.(*resourcepoolv1alpha1.ResourcePoolBinding)
	if !ok {
		return nil
	}
	namespace, enforceNamespace, err := o.getClientConfig().Namespace()
	if err != nil {
		return fmt.Errorf("resolve Kubernetes namespace: %w", err)
	}
	if binding.Namespace == "" {
		binding.Namespace = namespace
		return nil
	}
	if enforceNamespace && binding.Namespace != namespace {
		return fmt.Errorf(
			"ResourcePoolBinding namespace %q does not match the requested namespace %q",
			binding.Namespace,
			namespace,
		)
	}
	return nil
}

func newVersionCommand(options *commandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print hami-cli version information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Fprintln(options.out, currentCLIBuildInfo().String())
		},
	}
}

func pluginDisplayName(executable string) string {
	if executable == "" {
		executable = os.Args[0]
	}
	name := filepath.Base(executable)
	if !strings.HasPrefix(name, "kubectl-") {
		return ""
	}
	name = strings.TrimPrefix(name, "kubectl-")
	name = strings.ReplaceAll(name, "_", "-")
	return "kubectl " + name
}

func deviceNameFromArgs(args []string, nodeName, deviceID string) (string, error) {
	if len(args) == 1 {
		if nodeName != "" || deviceID != "" {
			return "", fmt.Errorf("DEVICE_NAME cannot be combined with --node or --device-id")
		}
		return args[0], nil
	}
	if nodeName == "" || deviceID == "" {
		return "", fmt.Errorf("provide DEVICE_NAME, or provide both --node and --device-id")
	}
	identity := resourcepoolv1alpha1.DeviceIdentity{NodeName: nodeName, DeviceType: resourcepoolv1alpha1.NVIDIADeviceType, DeviceID: deviceID}
	return resourcepoolv1alpha1.DeviceObjectName(identity), nil
}

func addOutputFlag(command *cobra.Command) {
	command.Flags().StringP("output", "o", "table", "Output format: table, name, json, or yaml")
}

func printObject(out io.Writer, output string, object any, names []string) error {
	switch output {
	case "name":
		for _, name := range names {
			fmt.Fprintln(out, name)
		}
		return nil
	case "json":
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(object)
	case "yaml":
		data, err := sigsyaml.Marshal(object)
		if err != nil {
			return fmt.Errorf("encode YAML output: %w", err)
		}
		_, err = out.Write(data)
		return err
	default:
		return fmt.Errorf("unsupported output format %q; choose table, name, json, or yaml", output)
	}
}

func unstructuredPoolNames(pools *unstructured.UnstructuredList) []string {
	names := make([]string, 0, len(pools.Items))
	for i := range pools.Items {
		names = append(names, "resourcepool.resource.hami.io/"+pools.Items[i].GetName())
	}
	return names
}

func deviceNames(devices *resourcepoolv1alpha1.GPUDeviceList) []string {
	names := make([]string, 0, len(devices.Items))
	for i := range devices.Items {
		names = append(names, "gpudevice.resource.hami.io/"+devices.Items[i].Name)
	}
	return names
}

func bindingNames(bindings *resourcepoolv1alpha1.ResourcePoolBindingList) []string {
	names := make([]string, 0, len(bindings.Items))
	for i := range bindings.Items {
		names = append(names, "resourcepoolbinding.resource.hami.io/"+bindings.Items[i].Name)
	}
	return names
}

func bindingConditionStatus(binding *resourcepoolv1alpha1.ResourcePoolBinding) string {
	for i := range binding.Status.Conditions {
		condition := &binding.Status.Conditions[i]
		if condition.Type == "Bound" {
			return string(condition.Status)
		}
	}
	return "Unknown"
}

func decodeResourcePoolObject(raw []byte) (client.Object, error) {
	typeMeta := &metav1.TypeMeta{}
	if err := json.Unmarshal(raw, typeMeta); err != nil {
		return nil, fmt.Errorf("decode object type: %w", err)
	}
	expectedVersion := resourcepoolv1alpha1.GroupVersion.String()
	if typeMeta.APIVersion != expectedVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q; expected %q", typeMeta.APIVersion, expectedVersion)
	}
	var object client.Object
	switch typeMeta.Kind {
	case "ResourcePool":
		// ResourcePool is decoded as unstructured so this standalone CLI preserves
		// fields introduced by a newer resource-pool API module when applying YAML.
		object = &unstructured.Unstructured{}
	case "GPUDevice":
		object = &resourcepoolv1alpha1.GPUDevice{}
	case "ResourcePoolBinding":
		object = &resourcepoolv1alpha1.ResourcePoolBinding{}
	default:
		return nil, fmt.Errorf("unsupported kind %q", typeMeta.Kind)
	}
	if err := json.Unmarshal(raw, object); err != nil {
		return nil, fmt.Errorf("decode %s: %w", typeMeta.Kind, err)
	}
	switch typed := object.(type) {
	case *resourcepoolv1alpha1.GPUDevice:
		typed.Labels = resourcepoolv1alpha1.PoolLabels(typed)
	case *resourcepoolv1alpha1.ResourcePoolBinding:
		if typed.Labels == nil {
			typed.Labels = make(map[string]string, 1)
		}
		typed.Labels[resourcepoolv1alpha1.PoolLabel] = typed.Spec.PoolRef.Name
	}
	object.GetObjectKind().SetGroupVersionKind(schema.FromAPIVersionAndKind(typeMeta.APIVersion, typeMeta.Kind))
	return object, nil
}
