//go:build !release_signed

package native

const (
	NativeDLLSize   = int64(67789312)
	NativeDLLSHA256 = "8C2C32BC5AC4F5D2E96AF10BFD2A6C1450D688DA91157B065E497B102945812C"

	// NativeArchiveSHA256 pins the published archive for NativeVersion on
	// windows/amd64. Release assets for a native version are immutable.
	NativeArchiveSHA256 = "A2F2BA223FA3803A90D1F16E5145C1D61FE2953EFEEB6AFBC6D912934F728EE6"
)
