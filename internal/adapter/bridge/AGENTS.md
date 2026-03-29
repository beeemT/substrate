# Bridge

This package provides shared infrastructure (`BridgeSession`, `BridgeRuntime`) embedded by the two concrete adapter bridges: `ohmypi` and `claudeagent`.

## Feature Parity

The two bridges must keep feature parity as far as the underlying backends allow.

- **Any bug fixed in one bridge MUST be investigated for the other bridge.** If the root cause applies there too, fix both in the same change.
- **Any capability added to one bridge MUST be evaluated for the other.** If the feature is applicable, implement it in both. If a genuine backend constraint prevents parity, document why in a code comment at the divergence point.
- When shared logic can be lifted into `BridgeSession` or `BridgeRuntime`, prefer that over duplicating the implementation across both adapters.
