// Package v1alpha1 defines the resource.hami.io/v1alpha1 objects used by hami-cli.
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var GroupVersion = schema.GroupVersion{Group: "resource.hami.io", Version: "v1alpha1"}

func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&ResourcePool{}, &ResourcePoolList{},
		&GPUDevice{}, &GPUDeviceList{},
		&ResourcePoolBinding{}, &ResourcePoolBindingList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

type PoolReference struct {
	Name string `json:"name"`
}

type NodeSchedulerPolicy string

const (
	NodeSchedulerPolicyBinpack NodeSchedulerPolicy = "binpack"
	NodeSchedulerPolicySpread  NodeSchedulerPolicy = "spread"
)

type GPUSchedulerPolicy string

const (
	GPUSchedulerPolicyBinpack       GPUSchedulerPolicy = "binpack"
	GPUSchedulerPolicySpread        GPUSchedulerPolicy = "spread"
	GPUSchedulerPolicyTopologyAware GPUSchedulerPolicy = "topology-aware"
	GPUSchedulerPolicyMutex         GPUSchedulerPolicy = "mutex"
	GPUSchedulerPolicyNUMA          GPUSchedulerPolicy = "numa"
)

type ResourcePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ResourcePoolSpec   `json:"spec"`
	Status            ResourcePoolStatus `json:"status,omitempty"`
}

type ResourcePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourcePool `json:"items"`
}

type ResourcePoolSpec struct {
	DisplayName      string                        `json:"displayName,omitempty"`
	WorkloadDefaults *ResourcePoolWorkloadDefaults `json:"workloadDefaults,omitempty"`
	Quota            *ResourcePoolQuotaSpec        `json:"quota,omitempty"`
}

type ResourcePoolWorkloadDefaults struct {
	SchedulingPolicy *ResourcePoolSchedulingPolicy `json:"schedulingPolicy,omitempty"`
}

type ResourcePoolSchedulingPolicy struct {
	Node NodeSchedulerPolicy `json:"node,omitempty"`
	GPU  GPUSchedulerPolicy  `json:"gpu,omitempty"`
}

type ResourcePoolQuotaSpec struct {
	CPU    *resource.Quantity `json:"cpu,omitempty"`
	Memory *resource.Quantity `json:"memory,omitempty"`
}

type ResourcePoolStatus struct {
	ObservedGeneration int64                    `json:"observedGeneration,omitempty"`
	DiscoveredDevices  int32                    `json:"discoveredDevices,omitempty"`
	HealthyDevices     int32                    `json:"healthyDevices,omitempty"`
	Revision           string                   `json:"revision,omitempty"`
	Quota              *ResourcePoolQuotaStatus `json:"quota,omitempty"`
	Conditions         []metav1.Condition       `json:"conditions,omitempty"`
}

type ResourcePoolQuotaStatus struct {
	ObservedGeneration int64                                `json:"observedGeneration,omitempty"`
	Used               ResourcePoolResourceUsage            `json:"used"`
	Reserved           ResourcePoolResourceUsage            `json:"reserved"`
	Reservations       []ResourcePoolQuotaReservationStatus `json:"reservations,omitempty"`
}

type ResourcePoolResourceUsage struct {
	CPU    resource.Quantity `json:"cpu,omitempty"`
	Memory resource.Quantity `json:"memory,omitempty"`
}

type ResourcePoolQuotaReservationStatus struct {
	ID        string                    `json:"id"`
	Namespace string                    `json:"namespace"`
	PodName   string                    `json:"podName"`
	PodUID    types.UID                 `json:"podUID,omitempty"`
	Delta     ResourcePoolResourceUsage `json:"delta"`
	Target    ResourcePoolResourceUsage `json:"target"`
	ExpiresAt metav1.Time               `json:"expiresAt"`
}

type GPUDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GPUDeviceSpec   `json:"spec"`
	Status            GPUDeviceStatus `json:"status,omitempty"`
}

type GPUDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUDevice `json:"items"`
}

type GPUDeviceSpec struct {
	Identity DeviceIdentity `json:"identity"`
	PoolRef  *PoolReference `json:"poolRef,omitempty"`
}

type DeviceIdentity struct {
	NodeName   string `json:"nodeName"`
	DeviceType string `json:"deviceType"`
	DeviceID   string `json:"deviceID"`
}

type GPUDeviceStatus struct {
	ObservedGeneration int64                 `json:"observedGeneration,omitempty"`
	Discovered         bool                  `json:"discovered"`
	Healthy            bool                  `json:"healthy"`
	Model              string                `json:"model,omitempty"`
	VGPUCount          int32                 `json:"vgpuCount,omitempty"`
	MemoryMiB          int32                 `json:"memoryMiB,omitempty"`
	CorePercent        int32                 `json:"corePercent,omitempty"`
	Conditions         []metav1.Condition    `json:"conditions,omitempty"`
	Allocations        []GPUAllocationStatus `json:"allocations,omitempty"`
}

type GPUAllocationState string

type GPUAllocationStatus struct {
	PodUID                   types.UID            `json:"podUID"`
	Namespace                string               `json:"namespace"`
	PodName                  string               `json:"podName"`
	ContainerName            string               `json:"containerName"`
	InitContainer            bool                 `json:"initContainer,omitempty"`
	RestartableInitContainer bool                 `json:"restartableInitContainer,omitempty"`
	ContainerIndex           int32                `json:"containerIndex"`
	DeviceIndex              int32                `json:"deviceIndex"`
	MemoryMiB                int32                `json:"memoryMiB,omitempty"`
	CorePercent              int32                `json:"corePercent,omitempty"`
	State                    GPUAllocationState   `json:"state"`
	MIG                      *MIGAllocationStatus `json:"mig,omitempty"`
}

type MIGAllocationStatus struct {
	Profile           string             `json:"profile"`
	Placement         MIGPlacementStatus `json:"placement"`
	MIGUUID           string             `json:"migUUID,omitempty"`
	GPUInstanceID     *uint32            `json:"gpuInstanceID,omitempty"`
	ComputeInstanceID *uint32            `json:"computeInstanceID,omitempty"`
}

type MIGPlacementStatus struct {
	Start uint32 `json:"start"`
	Size  uint32 `json:"size"`
}

type ResourcePoolBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ResourcePoolBindingSpec   `json:"spec"`
	Status            ResourcePoolBindingStatus `json:"status,omitempty"`
}

type ResourcePoolBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourcePoolBinding `json:"items"`
}

type ResourcePoolBindingSpec struct {
	PoolRef PoolReference `json:"poolRef"`
}

type ResourcePoolBindingStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}
