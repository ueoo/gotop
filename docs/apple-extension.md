# gotop Apple GPU extension

Provides Apple GPU data to gotop on macOS systems.

To enable it, either run gotop with the `--apple` flag, or add the line `apple=true` to `gotop.conf`.

## Dependencies

- macOS with Metal and IOKit (no extra packages required).

## Configuration

The refresh rate of Apple GPU data is controlled by the `applerefresh` parameter in the configuration file. This is a Go `time.Duration` format, for example `2s`, `500ms`, `1m`, etc.
