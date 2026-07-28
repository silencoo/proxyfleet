//go:build with_gvisor

package buildinfo

func init() {
	enableFeature("gvisor")
}
