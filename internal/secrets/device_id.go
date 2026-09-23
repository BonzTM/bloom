package secrets

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const deviceIDInfo = "bloom/jellyfin/device-id/v1"

// DeviceID derives a stable installation-specific UUID-shaped identifier.
func DeviceID(master []byte) (string, error) {
	if len(master) == 0 {
		return "", errors.New("derive device id: master secret is required")
	}
	value, err := hkdf.Key(sha256.New, master, nil, deviceIDInfo, 16)
	if err != nil {
		return "", fmt.Errorf("derive device id: %w", err)
	}
	defer clear(value)
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	return formatDeviceID(value), nil
}

func formatDeviceID(value []byte) string {
	var out [36]byte
	hex.Encode(out[0:8], value[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], value[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], value[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], value[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], value[10:16])
	return string(out[:])
}
