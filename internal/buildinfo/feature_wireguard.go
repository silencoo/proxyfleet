//go:build with_wireguard

package buildinfo

func init() {
	enableFeature("wireguard")
}
