package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"strings"
)

const (
	PoolLabel        = "resource.hami.io/pool"
	DeviceTypeLabel  = "resource.hami.io/device-type"
	NVIDIADeviceType = "NVIDIA"
)

// DeviceObjectName uses the RPM naming convention to look up existing devices.
func DeviceObjectName(identity DeviceIdentity) string {
	key := identity.NodeName + "\x00" + identity.DeviceType + "\x00" + identity.DeviceID
	digest := sha256.Sum256([]byte(key))
	return "gpu-" + hex.EncodeToString(digest[:16])
}

// PoolLabels keeps user labels and derives the indexes from the device spec.
func PoolLabels(device *GPUDevice) map[string]string {
	labels := make(map[string]string, len(device.Labels)+2)
	maps.Copy(labels, device.Labels)
	labels[DeviceTypeLabel] = strings.ToLower(device.Spec.Identity.DeviceType)
	if ref := device.Spec.PoolRef; ref != nil {
		labels[PoolLabel] = ref.Name
	} else {
		delete(labels, PoolLabel)
	}
	return labels
}
