//go:build with_grpc

package buildinfo

func init() {
	enableFeature("grpc")
}
