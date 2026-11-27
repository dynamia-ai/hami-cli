package v1alpha1

import (
	"slices"

	"k8s.io/apimachinery/pkg/runtime"
)

func (in *PoolReference) DeepCopy() *PoolReference {
	if in == nil {
		return nil
	}
	return new(*in)
}

func (in *ResourcePool) DeepCopy() *ResourcePool {
	if in == nil {
		return nil
	}
	out := new(ResourcePool)
	in.DeepCopyInto(out)
	return out
}

func (in *ResourcePool) DeepCopyInto(out *ResourcePool) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if defaults := in.Spec.WorkloadDefaults; defaults != nil {
		out.Spec.WorkloadDefaults = new(*defaults)
		if policy := defaults.SchedulingPolicy; policy != nil {
			out.Spec.WorkloadDefaults.SchedulingPolicy = new(*policy)
		}
	}
	if quota := in.Spec.Quota; quota != nil {
		out.Spec.Quota = new(*quota)
		if quota.CPU != nil {
			out.Spec.Quota.CPU = new(quota.CPU.DeepCopy())
		}
		if quota.Memory != nil {
			out.Spec.Quota.Memory = new(quota.Memory.DeepCopy())
		}
	}
	out.Status.Conditions = slices.Clone(in.Status.Conditions)
	if quota := in.Status.Quota; quota != nil {
		out.Status.Quota = new(*quota)
		out.Status.Quota.Used = quota.Used.deepCopy()
		out.Status.Quota.Reserved = quota.Reserved.deepCopy()
		out.Status.Quota.Reservations = slices.Clone(quota.Reservations)
		for i, reservation := range quota.Reservations {
			out.Status.Quota.Reservations[i].Delta = reservation.Delta.deepCopy()
			out.Status.Quota.Reservations[i].Target = reservation.Target.deepCopy()
		}
	}
}

func (in ResourcePoolResourceUsage) deepCopy() ResourcePoolResourceUsage {
	return ResourcePoolResourceUsage{CPU: in.CPU.DeepCopy(), Memory: in.Memory.DeepCopy()}
}

func (in *ResourcePool) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}

func (in *ResourcePoolList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(ResourcePoolList)
	in.DeepCopyInto(out)
	return out
}

func (in *ResourcePoolList) DeepCopyInto(out *ResourcePoolList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	out.Items = slices.Clone(in.Items)
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}

func (in *GPUDevice) DeepCopy() *GPUDevice {
	if in == nil {
		return nil
	}
	out := new(GPUDevice)
	in.DeepCopyInto(out)
	return out
}

func (in *GPUDevice) DeepCopyInto(out *GPUDevice) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec.PoolRef = in.Spec.PoolRef.DeepCopy()
	out.Status.Conditions = slices.Clone(in.Status.Conditions)
	out.Status.Allocations = slices.Clone(in.Status.Allocations)
	for i, allocation := range in.Status.Allocations {
		if mig := allocation.MIG; mig != nil {
			out.Status.Allocations[i].MIG = new(*mig)
			if mig.GPUInstanceID != nil {
				out.Status.Allocations[i].MIG.GPUInstanceID = new(*mig.GPUInstanceID)
			}
			if mig.ComputeInstanceID != nil {
				out.Status.Allocations[i].MIG.ComputeInstanceID = new(*mig.ComputeInstanceID)
			}
		}
	}
}

func (in *GPUDevice) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}

func (in *GPUDeviceList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(GPUDeviceList)
	in.DeepCopyInto(out)
	return out
}

func (in *GPUDeviceList) DeepCopyInto(out *GPUDeviceList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	out.Items = slices.Clone(in.Items)
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}

func (in *ResourcePoolBinding) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(ResourcePoolBinding)
	in.DeepCopyInto(out)
	return out
}

func (in *ResourcePoolBinding) DeepCopyInto(out *ResourcePoolBinding) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Status.Conditions = slices.Clone(in.Status.Conditions)
}

func (in *ResourcePoolBindingList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(ResourcePoolBindingList)
	in.DeepCopyInto(out)
	return out
}

func (in *ResourcePoolBindingList) DeepCopyInto(out *ResourcePoolBindingList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	out.Items = slices.Clone(in.Items)
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}
