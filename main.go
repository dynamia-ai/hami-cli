package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	resourcepoolv1alpha1 "github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api/v1alpha1"
)

type commandOptions struct {
	loadingRules      *clientcmd.ClientConfigLoadingRules
	overrides         *clientcmd.ConfigOverrides
	clientConfig      clientcmd.ClientConfig
	client            client.Client
	out               io.Writer
	errOut            io.Writer
	executable        string
	stdinTTY          bool
	kitupHome         string
	kitupCWD          string
	quotaPoll         quotaPollFunc
	quotaPollInterval time.Duration
}

type quotaPollFunc func(context.Context, time.Duration, time.Duration, bool, k8swait.ConditionWithContextFunc) error

const (
	defaultQuotaWaitTimeout  = 30 * time.Second
	defaultQuotaPollInterval = 500 * time.Millisecond
)

func main() {
	options := &commandOptions{
		out:      os.Stdout,
		errOut:   os.Stderr,
		stdinTTY: term.IsTerminal(int(os.Stdin.Fd())),
	}
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
		newApplyCommand(options),
		newSkillCommand(options),
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
		Args:    cobra.NoArgs,
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
			writer := newTableWriter(options.out)
			fmt.Fprintln(writer, "NAME\tCPU-QUOTA\tMEMORY-QUOTA\tDISCOVERED\tHEALTHY\tREVISION")
			for i := range pools.Items {
				pool := &pools.Items[i]
				cpuQuota, memoryQuota := poolQuotaValues(pool)
				fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%s\n", pool.Name, cpuQuota, memoryQuota, pool.Status.DiscoveredDevices, pool.Status.HealthyDevices, pool.Status.Revision)
			}
			return writer.Flush()
		},
	}
	addOutputFlag(list)
	command.AddCommand(
		list,
		newPoolBindingsCommand(options),
		newPoolBindCommand(options),
		newPoolDescribeCommand(options),
		newPoolUnbindCommand(options),
		newPoolUpdateCommand(options),
		newPoolCreateCommand(options),
		newPoolDeleteCommand(options),
	)
	return command
}

func newPoolCreateCommand(options *commandOptions) *cobra.Command {
	var cpuQuota string
	var memoryQuota string
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a resource pool",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(command *cobra.Command, _ []string) error {
			_, _, err := parsePoolQuotaFlags(command, cpuQuota, memoryQuota, false)
			return err
		},
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			cpu, memory, err := parsePoolQuotaFlags(command, cpuQuota, memoryQuota, false)
			if err != nil {
				return err
			}
			pool := &resourcepoolv1alpha1.ResourcePool{
				TypeMeta:   metav1.TypeMeta{APIVersion: resourcepoolv1alpha1.GroupVersion.String(), Kind: "ResourcePool"},
				ObjectMeta: metav1.ObjectMeta{Name: args[0]},
				Spec:       resourcepoolv1alpha1.ResourcePoolSpec{},
			}
			if cpu != nil || memory != nil {
				pool.Spec.Quota = &resourcepoolv1alpha1.ResourcePoolQuotaSpec{CPU: cpu, Memory: memory}
			}
			if err := cli.Create(command.Context(), pool); err != nil {
				return fmt.Errorf("create resource pool %q: %w", args[0], err)
			}
			fmt.Fprintf(options.out, "resourcepool.resource.hami.io/%s created\n", args[0])
			return nil
		},
	}
	addPoolQuotaFlags(command, &cpuQuota, &memoryQuota, false)
	return command
}

func newPoolUpdateCommand(options *commandOptions) *cobra.Command {
	var nodePolicy string
	var gpuPolicy string
	var cpuQuota string
	var memoryQuota string
	var waitForCurrent bool
	var waitTimeout time.Duration
	command := &cobra.Command{
		Use:   "update NAME",
		Short: "Update a resource pool's workload defaults and quotas",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(command *cobra.Command, _ []string) error {
			nodeChanged := command.Flags().Changed("node-scheduler-policy")
			gpuChanged := command.Flags().Changed("gpu-scheduler-policy")
			if !nodeChanged && !gpuChanged && !command.Flags().Changed("cpu-quota") && !command.Flags().Changed("memory-quota") {
				return errors.New("at least one scheduling-policy or quota flag must be specified")
			}
			if command.Flags().Changed("wait-timeout") && !waitForCurrent {
				return errors.New("--wait-timeout requires --wait-for-current")
			}
			if waitForCurrent && waitTimeout <= 0 {
				return errors.New("--wait-timeout must be greater than zero")
			}
			if nodeChanged {
				if err := validateNodeSchedulerPolicy(nodePolicy); err != nil {
					return err
				}
			}
			if gpuChanged {
				if err := validateGPUSchedulerPolicy(gpuPolicy); err != nil {
					return err
				}
			}
			_, _, err := parsePoolQuotaFlags(command, cpuQuota, memoryQuota, true)
			return err
		},
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			poolName := args[0]
			pool := &resourcepoolv1alpha1.ResourcePool{}
			if err := cli.Get(command.Context(), client.ObjectKey{Name: poolName}, pool); err != nil {
				return fmt.Errorf("get resource pool %q: %w", poolName, err)
			}

			base := pool.DeepCopy()
			if command.Flags().Changed("node-scheduler-policy") || command.Flags().Changed("gpu-scheduler-policy") {
				if pool.Spec.WorkloadDefaults == nil {
					pool.Spec.WorkloadDefaults = &resourcepoolv1alpha1.ResourcePoolWorkloadDefaults{}
				}
				if pool.Spec.WorkloadDefaults.SchedulingPolicy == nil {
					pool.Spec.WorkloadDefaults.SchedulingPolicy = &resourcepoolv1alpha1.ResourcePoolSchedulingPolicy{}
				}
				policy := pool.Spec.WorkloadDefaults.SchedulingPolicy
				if command.Flags().Changed("node-scheduler-policy") {
					policy.Node = resourcepoolv1alpha1.NodeSchedulerPolicy(nodePolicy)
				}
				if command.Flags().Changed("gpu-scheduler-policy") {
					policy.GPU = resourcepoolv1alpha1.GPUSchedulerPolicy(gpuPolicy)
				}
			}

			cpu, memory, err := parsePoolQuotaFlags(command, cpuQuota, memoryQuota, true)
			if err != nil {
				return err
			}
			if command.Flags().Changed("cpu-quota") || command.Flags().Changed("memory-quota") {
				if pool.Spec.Quota == nil {
					pool.Spec.Quota = &resourcepoolv1alpha1.ResourcePoolQuotaSpec{}
				}
				if command.Flags().Changed("cpu-quota") {
					pool.Spec.Quota.CPU = cpu
				}
				if command.Flags().Changed("memory-quota") {
					pool.Spec.Quota.Memory = memory
				}
				if pool.Spec.Quota.CPU == nil && pool.Spec.Quota.Memory == nil {
					pool.Spec.Quota = nil
				}
			}

			if err := cli.Patch(command.Context(), pool, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				return fmt.Errorf("update resource pool %q: %w", poolName, err)
			}
			fmt.Fprintf(options.out, "resourcepool.resource.hami.io/%s updated\n", poolName)
			if waitForCurrent {
				if err := options.waitForPoolQuotaCurrent(command.Context(), cli, poolName, pool.UID, pool.Generation, waitTimeout); err != nil {
					return err
				}
				fmt.Fprintf(options.out, "resourcepool.resource.hami.io/%s quota status current\n", poolName)
			}
			return nil
		},
	}
	command.Flags().StringVar(&nodePolicy, "node-scheduler-policy", "", "Node scheduling policy: binpack or spread")
	command.Flags().StringVar(&gpuPolicy, "gpu-scheduler-policy", "", "Ordered, comma-separated GPU policies: binpack, spread, topology-aware, mutex, or numa")
	addPoolQuotaFlags(command, &cpuQuota, &memoryQuota, true)
	command.Flags().BoolVar(&waitForCurrent, "wait-for-current", false, "Wait for quota status to reflect the updated ResourcePool generation")
	command.Flags().DurationVar(&waitTimeout, "wait-timeout", defaultQuotaWaitTimeout, "Maximum total time to wait for quota status after updating")
	return command
}

func (options *commandOptions) waitForPoolQuotaCurrent(
	ctx context.Context,
	cli client.Client,
	poolName string,
	expectedUID types.UID,
	expectedGeneration int64,
	timeout time.Duration,
) error {
	poll := options.quotaPoll
	if poll == nil {
		poll = k8swait.PollUntilContextTimeout
	}
	interval := options.quotaPollInterval
	if interval <= 0 {
		interval = defaultQuotaPollInterval
	}
	lastState := "not observed"
	err := poll(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		pool := &resourcepoolv1alpha1.ResourcePool{}
		if err := cli.Get(ctx, client.ObjectKey{Name: poolName}, pool); err != nil {
			return false, fmt.Errorf("read resource pool %q while waiting for quota status: %w", poolName, err)
		}
		if expectedUID != "" && pool.UID != expectedUID {
			return false, fmt.Errorf("resource pool %q was replaced while waiting for quota status: UID is %q, expected %q", poolName, pool.UID, expectedUID)
		}
		if pool.Generation != expectedGeneration {
			return false, fmt.Errorf("resource pool %q changed while waiting for quota status: generation is %d, expected %d", poolName, pool.Generation, expectedGeneration)
		}

		lastState = quotaWaitState(pool)
		if pool.Spec.Quota == nil {
			return pool.Status.Quota == nil, nil
		}
		return pool.Status.Quota != nil && pool.Status.Quota.ObservedGeneration == expectedGeneration, nil
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("wait up to %s for resource pool %q quota status: %w (last state: %s)", timeout, poolName, err, lastState)
	}
	return fmt.Errorf("wait for resource pool %q quota status: %w", poolName, err)
}

func quotaWaitState(pool *resourcepoolv1alpha1.ResourcePool) string {
	if pool.Spec.Quota == nil && pool.Status.Quota != nil {
		return fmt.Sprintf("pending removal (status observed generation %d, current generation %d)", pool.Status.Quota.ObservedGeneration, pool.Generation)
	}
	if state := quotaLedgerState(pool); state != "" {
		return state
	}
	return fmt.Sprintf("pending (current generation %d)", pool.Generation)
}

func addPoolQuotaFlags(command *cobra.Command, cpuQuota, memoryQuota *string, allowRemoval bool) {
	cpuUsage := "Optional aggregate CPU limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling"
	memoryUsage := "Optional aggregate memory limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling"
	if allowRemoval {
		cpuUsage = "Aggregate CPU limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling, unlimited removes the limit"
		memoryUsage = "Aggregate memory limit ceiling as a Kubernetes quantity; 0 is a hard zero ceiling, unlimited removes the limit"
	}
	command.Flags().StringVar(cpuQuota, "cpu-quota", "", cpuUsage)
	command.Flags().StringVar(memoryQuota, "memory-quota", "", memoryUsage)
}

func parsePoolQuotaFlags(command *cobra.Command, cpuValue, memoryValue string, allowUnlimited bool) (*apiresource.Quantity, *apiresource.Quantity, error) {
	var cpu *apiresource.Quantity
	var memory *apiresource.Quantity
	var err error
	if command.Flags().Changed("cpu-quota") {
		cpu, err = parsePoolQuota("CPU", cpuValue, allowUnlimited)
		if err != nil {
			return nil, nil, err
		}
	}
	if command.Flags().Changed("memory-quota") {
		memory, err = parsePoolQuota("memory", memoryValue, allowUnlimited)
		if err != nil {
			return nil, nil, err
		}
	}
	return cpu, memory, nil
}

func parsePoolQuota(resourceName, value string, allowUnlimited bool) (*apiresource.Quantity, error) {
	if value == "unlimited" {
		if !allowUnlimited {
			return nil, fmt.Errorf("%s quota %q is only valid for pool update", resourceName, value)
		}
		return nil, nil
	}
	quantity, err := apiresource.ParseQuantity(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s quota %q: %w", resourceName, value, err)
	}
	if quantity.Sign() < 0 {
		return nil, fmt.Errorf("%s quota must not be negative", resourceName)
	}
	return &quantity, nil
}

func poolQuotaValues(pool *resourcepoolv1alpha1.ResourcePool) (string, string) {
	cpu, memory := "<unlimited>", "<unlimited>"
	if pool.Spec.Quota == nil {
		return cpu, memory
	}
	if pool.Spec.Quota.CPU != nil {
		cpu = pool.Spec.Quota.CPU.String()
	}
	if pool.Spec.Quota.Memory != nil {
		memory = pool.Spec.Quota.Memory.String()
	}
	return cpu, memory
}

func validateNodeSchedulerPolicy(policy string) error {
	switch resourcepoolv1alpha1.NodeSchedulerPolicy(policy) {
	case resourcepoolv1alpha1.NodeSchedulerPolicyBinpack, resourcepoolv1alpha1.NodeSchedulerPolicySpread:
		return nil
	default:
		return fmt.Errorf("invalid node scheduler policy %q; must be binpack or spread", policy)
	}
}

func validateGPUSchedulerPolicy(policy string) error {
	validPolicies := map[resourcepoolv1alpha1.GPUSchedulerPolicy]struct{}{
		resourcepoolv1alpha1.GPUSchedulerPolicyBinpack:       {},
		resourcepoolv1alpha1.GPUSchedulerPolicySpread:        {},
		resourcepoolv1alpha1.GPUSchedulerPolicyTopologyAware: {},
		resourcepoolv1alpha1.GPUSchedulerPolicyMutex:         {},
		resourcepoolv1alpha1.GPUSchedulerPolicyNUMA:          {},
	}
	for policyPart := range strings.SplitSeq(policy, ",") {
		if _, ok := validPolicies[resourcepoolv1alpha1.GPUSchedulerPolicy(policyPart)]; !ok {
			return fmt.Errorf(
				"invalid GPU scheduler policy %q in %q; must be binpack, spread, topology-aware, mutex, or numa",
				policyPart,
				policy,
			)
		}
	}
	return nil
}

func newPoolDescribeCommand(options *commandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "describe NAME",
		Short: "Show a resource pool and its namespace and device associations",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			poolName := args[0]
			pool := &resourcepoolv1alpha1.ResourcePool{}
			if err := cli.Get(command.Context(), client.ObjectKey{Name: poolName}, pool); err != nil {
				return fmt.Errorf("get resource pool %q: %w", poolName, err)
			}

			bindings := &resourcepoolv1alpha1.ResourcePoolBindingList{}
			if err := cli.List(command.Context(), bindings); err != nil {
				return fmt.Errorf("list resource-pool bindings for resource pool %q: %w", poolName, err)
			}
			matchingBindings := bindings.Items[:0]
			for i := range bindings.Items {
				if bindings.Items[i].Spec.PoolRef.Name == poolName {
					matchingBindings = append(matchingBindings, bindings.Items[i])
				}
			}
			bindings.Items = matchingBindings
			sort.Slice(bindings.Items, func(i, j int) bool {
				if bindings.Items[i].Namespace == bindings.Items[j].Namespace {
					return bindings.Items[i].Name < bindings.Items[j].Name
				}
				return bindings.Items[i].Namespace < bindings.Items[j].Namespace
			})

			devices := &resourcepoolv1alpha1.GPUDeviceList{}
			if err := cli.List(command.Context(), devices); err != nil {
				return fmt.Errorf("list GPU devices for resource pool %q: %w", poolName, err)
			}
			matchingDevices := devices.Items[:0]
			for i := range devices.Items {
				if gpuDevicePoolName(&devices.Items[i]) == poolName {
					matchingDevices = append(matchingDevices, devices.Items[i])
				}
			}
			devices.Items = matchingDevices
			sort.Slice(devices.Items, func(i, j int) bool {
				left, right := &devices.Items[i], &devices.Items[j]
				if left.Spec.Identity.NodeName != right.Spec.Identity.NodeName {
					return left.Spec.Identity.NodeName < right.Spec.Identity.NodeName
				}
				if left.Spec.Identity.DeviceID != right.Spec.Identity.DeviceID {
					return left.Spec.Identity.DeviceID < right.Spec.Identity.DeviceID
				}
				return left.Name < right.Name
			})

			return printPoolDescription(options.out, pool, bindings, devices)
		},
	}
}

func printPoolDescription(out io.Writer, pool *resourcepoolv1alpha1.ResourcePool, bindings *resourcepoolv1alpha1.ResourcePoolBindingList, devices *resourcepoolv1alpha1.GPUDeviceList) error {
	nodePolicy, gpuPolicy := "<unset>", "<unset>"
	cpuQuota, memoryQuota := poolQuotaValues(pool)
	if pool.Spec.WorkloadDefaults != nil && pool.Spec.WorkloadDefaults.SchedulingPolicy != nil {
		policy := pool.Spec.WorkloadDefaults.SchedulingPolicy
		if policy.Node != "" {
			nodePolicy = string(policy.Node)
		}
		if policy.GPU != "" {
			gpuPolicy = string(policy.GPU)
		}
	}
	revision := pool.Status.Revision
	if revision == "" {
		revision = "<none>"
	}

	writer := newTableWriter(out)
	fmt.Fprintf(writer, "Name:\t%s\n", pool.Name)
	fmt.Fprintf(writer, "CPU quota:\t%s\n", cpuQuota)
	fmt.Fprintf(writer, "Memory quota:\t%s\n", memoryQuota)
	if ledger := quotaLedgerState(pool); ledger != "" {
		fmt.Fprintf(writer, "Quota ledger:\t%s\n", ledger)
	}
	if pool.Status.Quota != nil {
		fmt.Fprintf(writer, "CPU used:\t%s\n", pool.Status.Quota.Used.CPU.String())
		fmt.Fprintf(writer, "CPU reserved:\t%s\n", pool.Status.Quota.Reserved.CPU.String())
		fmt.Fprintf(writer, "Memory used:\t%s\n", pool.Status.Quota.Used.Memory.String())
		fmt.Fprintf(writer, "Memory reserved:\t%s\n", pool.Status.Quota.Reserved.Memory.String())
	}
	fmt.Fprintf(writer, "Node scheduler policy:\t%s\n", nodePolicy)
	fmt.Fprintf(writer, "GPU scheduler policy:\t%s\n", gpuPolicy)
	fmt.Fprintf(writer, "Discovered devices:\t%d\n", pool.Status.DiscoveredDevices)
	fmt.Fprintf(writer, "Healthy devices:\t%d\n", pool.Status.HealthyDevices)
	fmt.Fprintf(writer, "Revision:\t%s\n", revision)
	if err := writer.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nNamespaces:")
	if len(bindings.Items) == 0 {
		fmt.Fprintln(out, "  <none>")
	} else {
		writer = newTableWriter(out)
		fmt.Fprintln(writer, "  NAMESPACE\tBINDING\tBOUND")
		for i := range bindings.Items {
			binding := &bindings.Items[i]
			fmt.Fprintf(writer, "  %s\t%s\t%s\n", binding.Namespace, binding.Name, bindingConditionStatus(binding))
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "\nDevices:")
	if len(devices.Items) == 0 {
		fmt.Fprintln(out, "  <none>")
		return nil
	}
	writer = newTableWriter(out)
	fmt.Fprintln(writer, "  NAME\tNODE\tDEVICE-ID\tDISCOVERED\tHEALTHY")
	for i := range devices.Items {
		device := &devices.Items[i]
		fmt.Fprintf(writer, "  %s\t%s\t%s\t%t\t%t\n", device.Name, device.Spec.Identity.NodeName, device.Spec.Identity.DeviceID, device.Status.Discovered, device.Status.Healthy)
	}
	return writer.Flush()
}

func quotaLedgerState(pool *resourcepoolv1alpha1.ResourcePool) string {
	if pool.Status.Quota == nil {
		if pool.Spec.Quota == nil {
			return ""
		}
		return fmt.Sprintf("pending (current generation %d)", pool.Generation)
	}
	observed := pool.Status.Quota.ObservedGeneration
	if observed == pool.Generation {
		return fmt.Sprintf("current (generation %d)", observed)
	}
	return fmt.Sprintf("stale (observed generation %d, current generation %d)", observed, pool.Generation)
}

func newPoolDeleteCommand(options *commandOptions) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a resource pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			poolName := args[0]
			pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: poolName}}
			if force {
				if err := cli.Get(command.Context(), client.ObjectKey{Name: poolName}, pool); err != nil {
					return fmt.Errorf("get resource pool %q before forced deletion: %w", poolName, err)
				}
				if pool.UID == "" {
					return fmt.Errorf("resource pool %q has no UID", poolName)
				}
				if err := deleteResourcePoolReferences(command.Context(), cli, options.out, pool); err != nil {
					return fmt.Errorf("remove references to resource pool %q: %w", poolName, err)
				}
			}
			deleteOptions := []client.DeleteOption{}
			if force {
				deleteOptions = append(deleteOptions, client.Preconditions{UID: &pool.UID})
			}
			if err := cli.Delete(command.Context(), pool, deleteOptions...); err != nil && (!force || !apierrors.IsNotFound(err)) {
				return fmt.Errorf("delete resource pool %q: %w", poolName, err)
			}
			fmt.Fprintf(options.out, "resourcepool.resource.hami.io/%s deleted\n", poolName)
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Delete ResourcePoolBindings and unassign GPUDevices that reference the pool")
	return command
}

const maxPoolReferenceCleanupPasses = 5

func deleteResourcePoolReferences(ctx context.Context, cli client.Client, out io.Writer, pool *resourcepoolv1alpha1.ResourcePool) error {
	for cleanupPass := 0; ; cleanupPass++ {
		bindings := &resourcepoolv1alpha1.ResourcePoolBindingList{}
		if err := cli.List(ctx, bindings); err != nil {
			return fmt.Errorf("list ResourcePoolBindings: %w", err)
		}
		devices := &resourcepoolv1alpha1.GPUDeviceList{}
		if err := cli.List(ctx, devices); err != nil {
			return fmt.Errorf("list GPUDevices: %w", err)
		}
		sort.Slice(bindings.Items, func(i, j int) bool {
			if bindings.Items[i].Namespace == bindings.Items[j].Namespace {
				return bindings.Items[i].Name < bindings.Items[j].Name
			}
			return bindings.Items[i].Namespace < bindings.Items[j].Namespace
		})
		sort.Slice(devices.Items, func(i, j int) bool { return devices.Items[i].Name < devices.Items[j].Name })

		matchingBindings := 0
		for i := range bindings.Items {
			if bindings.Items[i].Spec.PoolRef.Name == pool.Name {
				matchingBindings++
			}
		}
		matchingDevices := 0
		for i := range devices.Items {
			if gpuDevicePoolName(&devices.Items[i]) == pool.Name {
				matchingDevices++
			}
		}
		currentPool := &resourcepoolv1alpha1.ResourcePool{}
		if err := cli.Get(ctx, client.ObjectKey{Name: pool.Name}, currentPool); err != nil {
			if apierrors.IsNotFound(err) && matchingBindings == 0 && matchingDevices == 0 {
				return nil
			}
			return fmt.Errorf("re-read ResourcePool: %w", err)
		}
		if currentPool.UID != pool.UID {
			return fmt.Errorf("ResourcePool UID is %q, expected %q", currentPool.UID, pool.UID)
		}
		if matchingBindings == 0 && matchingDevices == 0 {
			return nil
		}
		if cleanupPass >= maxPoolReferenceCleanupPasses {
			return fmt.Errorf(
				"references did not stabilize after %d cleanup passes: %d ResourcePoolBindings and %d GPUDevices still reference the pool",
				maxPoolReferenceCleanupPasses,
				matchingBindings,
				matchingDevices,
			)
		}

		for i := range bindings.Items {
			binding := &bindings.Items[i]
			if binding.Spec.PoolRef.Name != pool.Name {
				continue
			}
			if binding.UID == "" {
				return fmt.Errorf("ResourcePoolBinding %s/%s has no UID", binding.Namespace, binding.Name)
			}
			if err := cli.Delete(ctx, binding, client.Preconditions{UID: &binding.UID}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete ResourcePoolBinding %s/%s: %w", binding.Namespace, binding.Name, err)
			}
			fmt.Fprintf(out, "resourcepoolbinding.resource.hami.io/%s deleted from namespace %s\n", binding.Name, binding.Namespace)
		}

		for i := range devices.Items {
			device := &devices.Items[i]
			if gpuDevicePoolName(device) != pool.Name {
				continue
			}
			base := device.DeepCopy()
			device.Spec.PoolRef = nil
			device.Labels = resourcepoolv1alpha1.PoolLabels(device)
			if err := cli.Patch(ctx, device, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				if apierrors.IsNotFound(err) {
					fmt.Fprintf(out, "gpudevice.resource.hami.io/%s already absent\n", device.Name)
					continue
				}
				return fmt.Errorf("unassign GPUDevice %q: %w", device.Name, err)
			}
			fmt.Fprintf(out, "gpudevice.resource.hami.io/%s unassigned\n", device.Name)
		}
	}
}

func newDeviceCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{Use: "device", Short: "Inspect and assign discovered GPUDevice objects"}
	var nodeName string
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List discovered physical GPUs",
		Args:    cobra.NoArgs,
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
			writer := newTableWriter(options.out)
			fmt.Fprintln(writer, "NAME\tNODE\tDEVICE-ID\tPOOL\tDISCOVERED\tHEALTHY")
			for i := range devices.Items {
				device := &devices.Items[i]
				pool := "<unassigned>"
				if device.Spec.PoolRef != nil {
					pool = device.Spec.PoolRef.Name
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%t\t%t\n", device.Name, device.Spec.Identity.NodeName, device.Spec.Identity.DeviceID, pool, device.Status.Discovered, device.Status.Healthy)
			}
			return writer.Flush()
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
	var nodeName, deviceID, expectedUID, expectedPool string
	command := &cobra.Command{
		Use:   "unassign [DEVICE_NAME]",
		Short: "Remove one physical GPU from its resource pool",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			hasExpectedUID := command.Flags().Changed("expected-uid")
			hasExpectedPool := command.Flags().Changed("expected-pool")
			if hasExpectedUID != hasExpectedPool {
				return fmt.Errorf("--expected-uid and --expected-pool must be used together")
			}
			if hasExpectedUID && (expectedUID == "" || expectedPool == "") {
				return fmt.Errorf("--expected-uid and --expected-pool must not be empty")
			}
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
			if hasExpectedUID && string(device.UID) != expectedUID {
				return fmt.Errorf("GPUDevice %q UID is %q, expected %q", name, device.UID, expectedUID)
			}
			if hasExpectedPool && gpuDevicePoolName(device) != expectedPool {
				return fmt.Errorf("GPUDevice %q pool is %q, expected %q", name, gpuDevicePoolName(device), expectedPool)
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
	command.Flags().StringVar(&expectedUID, "expected-uid", "", "Require the current GPUDevice UID before unassigning")
	command.Flags().StringVar(&expectedPool, "expected-pool", "", "Require the current ResourcePool name before unassigning")
	return command
}

func newPoolBindingsCommand(options *commandOptions) *cobra.Command {
	var allPools bool
	command := &cobra.Command{
		Use:   "bindings [NAME]",
		Short: "List namespace bindings for one or all resource pools",
		Args:  cobra.MaximumNArgs(1),
		PreRunE: func(command *cobra.Command, args []string) error {
			if command.Flags().Changed("namespace") {
				return errors.New("--namespace cannot be used with pool bindings; results always span namespaces")
			}
			if len(args) == 0 && !allPools {
				return errors.New("either NAME or --all-pools must be specified")
			}
			if len(args) == 1 && allPools {
				return errors.New("NAME cannot be combined with --all-pools")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			poolName := ""
			if len(args) == 1 {
				poolName = args[0]
			}
			bindings := &resourcepoolv1alpha1.ResourcePoolBindingList{}
			if err := cli.List(command.Context(), bindings); err != nil {
				if allPools {
					return fmt.Errorf("list resource-pool bindings: %w", err)
				}
				return fmt.Errorf("list bindings for resource pool %q: %w", poolName, err)
			}
			if !allPools {
				matchingBindings := bindings.Items[:0]
				for i := range bindings.Items {
					if bindings.Items[i].Spec.PoolRef.Name == poolName {
						matchingBindings = append(matchingBindings, bindings.Items[i])
					}
				}
				bindings.Items = matchingBindings
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
			writer := newTableWriter(options.out)
			if allPools {
				fmt.Fprintln(writer, "NAMESPACE\tBINDING\tPOOL\tBOUND")
				for i := range bindings.Items {
					binding := &bindings.Items[i]
					fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", binding.Namespace, binding.Name, binding.Spec.PoolRef.Name, bindingConditionStatus(binding))
				}
				return writer.Flush()
			}
			fmt.Fprintln(writer, "NAMESPACE\tBINDING\tBOUND")
			for i := range bindings.Items {
				binding := &bindings.Items[i]
				fmt.Fprintf(writer, "%s\t%s\t%s\n", binding.Namespace, binding.Name, bindingConditionStatus(binding))
			}
			return writer.Flush()
		},
	}
	command.Flags().BoolVarP(&allPools, "all-pools", "A", false, "List bindings for all resource pools")
	addOutputFlag(command)
	return command
}

func newTableWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
}

func newPoolBindCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "bind NAME --namespace NAMESPACE",
		Short: "Grant a namespace access to a resource pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			namespace, err := options.namespace()
			if err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			poolName := args[0]
			binding := &resourcepoolv1alpha1.ResourcePoolBinding{
				TypeMeta: metav1.TypeMeta{APIVersion: resourcepoolv1alpha1.GroupVersion.String(), Kind: "ResourcePoolBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
					Labels:    map[string]string{resourcepoolv1alpha1.PoolLabel: poolName},
				},
				Spec: resourcepoolv1alpha1.ResourcePoolBindingSpec{PoolRef: resourcepoolv1alpha1.PoolReference{Name: poolName}},
			}
			if err := cli.Create(command.Context(), binding); err != nil {
				return fmt.Errorf("bind namespace %q to resource pool %q: %w", namespace, poolName, err)
			}
			fmt.Fprintf(options.out, "resourcepoolbinding.resource.hami.io/%s created in namespace %s\n", poolName, namespace)
			return nil
		},
	}
	return command
}

func newPoolUnbindCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "unbind NAME --namespace NAMESPACE",
		Short: "Revoke a namespace's resource-pool access for future Pods",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			namespace, err := options.namespace()
			if err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			poolName := args[0]
			binding := &resourcepoolv1alpha1.ResourcePoolBinding{ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: namespace}}
			if err := cli.Delete(command.Context(), binding); err != nil {
				return fmt.Errorf("unbind namespace %q from resource pool %q: %w", namespace, poolName, err)
			}
			fmt.Fprintf(options.out, "resourcepoolbinding.resource.hami.io/%s deleted from namespace %s\n", poolName, namespace)
			return nil
		},
	}
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
	if err := decodeObject(raw, object, typeMeta.Kind != "ResourcePool"); err != nil {
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

func decodeObject(raw []byte, object client.Object, strict bool) error {
	if !strict {
		return json.Unmarshal(raw, object)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(object); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return errors.New("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return nil
}
