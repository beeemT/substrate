package buildinfo

// Version is the user-visible Substrate version.
// Release builds override this with -ldflags "-X github.com/beeemT/substrate/internal/buildinfo.Version=<version>".
var Version = "dev"

// BuildSHA is the source revision for this binary.
// Release builds override this with -ldflags "-X github.com/beeemT/substrate/internal/buildinfo.BuildSHA=<sha>".
var BuildSHA = ""

// BuildTime is the RFC3339 timestamp at which the binary was built.
// Release builds override this with -ldflags "-X github.com/beeemT/substrate/internal/buildinfo.BuildTime=<rfc3339>".
var BuildTime = ""
