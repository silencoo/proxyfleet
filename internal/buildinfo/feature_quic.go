//go:build with_quic

package buildinfo

func init() {
	enableFeature("quic")
}
