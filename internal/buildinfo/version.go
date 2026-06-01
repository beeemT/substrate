package buildinfo

// Version is the user-visible Substrate version.
// Release builds override this with -ldflags "-X github.com/beeemT/substrate/internal/buildinfo.Version=<version>".
var Version = "dev"
