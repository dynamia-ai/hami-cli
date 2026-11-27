package v1alpha1

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func TestResourcePoolObjectsRoundTrip(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	decoder := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	objects := []string{
		`{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePool","metadata":{"name":"training","creationTimestamp":"2026-11-26T10:00:00Z"},
		"spec":{"quota":{"cpu":"1500m","memory":"2Gi"},"workloadDefaults":{"schedulingPolicy":{"node":"spread","gpu":"topology-aware"}}},
		"status":{"quota":{"observedGeneration":2,"used":{"cpu":"1","memory":"1Gi"},"reserved":{"cpu":"500m","memory":"1Gi"},
		"reservations":[{"id":"r1","namespace":"ml","podName":"trainer","podUID":"pod-1",
		"delta":{"cpu":"500m","memory":"1Gi"},"target":{"cpu":"1","memory":"2Gi"},"expiresAt":"2026-11-27T09:00:00Z"}]}}}`,
		`{"apiVersion":"resource.hami.io/v1alpha1","kind":"GPUDevice","metadata":{"name":"gpu-a","creationTimestamp":"2026-11-26T10:00:00Z"},
		"spec":{"identity":{"nodeName":"node-a","deviceType":"NVIDIA","deviceID":"GPU-A"},"poolRef":{"name":"training"}},
		"status":{"discovered":true,"healthy":true,"model":"A100","vgpuCount":4,"memoryMiB":40960,"corePercent":100,
		"allocations":[{"podUID":"pod-1","namespace":"ml","podName":"trainer","containerName":"init-model",
		"initContainer":true,"restartableInitContainer":true,"containerIndex":0,"deviceIndex":0,"memoryMiB":5120,
		"corePercent":20,"state":"Active","mig":{"profile":"1g.5gb","placement":{"start":0,"size":1},
		"migUUID":"MIG-A","gpuInstanceID":0,"computeInstanceID":0}}]}}`,
		`{"apiVersion":"resource.hami.io/v1alpha1","kind":"ResourcePoolBinding","metadata":{"name":"training","namespace":"ml","creationTimestamp":"2026-11-26T10:00:00Z"},
		"spec":{"poolRef":{"name":"training"}},"status":{"observedGeneration":1}}`,
	}
	for _, raw := range objects {
		obj, gvk, err := decoder.Decode([]byte(raw), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(gvk.Kind, func(t *testing.T) {
			t.Parallel()
			copy := obj.DeepCopyObject()
			encoded, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal([]byte(raw), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip changed object\ngot:  %s\nwant: %s", encoded, raw)
			}
			// Patch bases and objects stored in the client must own nested data.
			adjustment := resource.MustParse("100m")
			switch copied := copy.(type) {
			case *ResourcePool:
				copied.Spec.Quota.CPU.Sub(adjustment)
				copied.Status.Quota.Reservations[0].Delta.CPU.Sub(adjustment)
			case *GPUDevice:
				copied.Spec.PoolRef.Name = "other"
				*copied.Status.Allocations[0].MIG.GPUInstanceID = 3
			case *ResourcePoolBinding:
				copied.Spec.PoolRef.Name = "other"
			}
			original, err := json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			if string(original) != string(encoded) {
				t.Errorf("changing the copy changed the source object: %s", original)
			}
		})
	}
}

func TestGPUDeviceIdentityAndPoolIndexes(t *testing.T) {
	t.Parallel()
	device := &GPUDevice{Spec: GPUDeviceSpec{
		Identity: DeviceIdentity{NodeName: "node-a", DeviceType: "NVIDIA", DeviceID: "GPU-A"},
		PoolRef:  &PoolReference{Name: "training"},
	}}
	device.Labels = map[string]string{"team": "ml", "resource.hami.io/pool": "old"}
	const wantName = "gpu-5aea37ceecac0d037b703769861f1980"
	if got := DeviceObjectName(device.Spec.Identity); got != wantName {
		t.Errorf("DeviceObjectName() = %q, want %q", got, wantName)
	}
	want := map[string]string{"team": "ml", "resource.hami.io/device-type": "nvidia", "resource.hami.io/pool": "training"}
	if got := PoolLabels(device); !maps.Equal(got, want) {
		t.Errorf("PoolLabels() = %v, want %v", got, want)
	}
	device.Spec.PoolRef = nil
	delete(want, "resource.hami.io/pool")
	if got := PoolLabels(device); !maps.Equal(got, want) {
		t.Errorf("PoolLabels() after unassign = %v, want %v", got, want)
	}
	if got := device.Labels["resource.hami.io/pool"]; got != "old" {
		t.Errorf("PoolLabels() changed source label to %q", got)
	}
}
