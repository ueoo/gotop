# gotop AMD extension

Provides AMD GPU data to gotop on Linux systems.

To enable it, either run gotop with the `--amd` flag, or add the line `amd=true` to `gotop.conf`.

## Dependencies

- Linux kernel with `amdgpu` driver and sysfs GPU metrics (e.g. `/sys/class/drm/card*/device`).

## Configuration

The refresh rate of AMD data is controlled by the `amdrefresh` parameter in the configuration file. This is a Go `time.Duration` format, for example `2s`, `500ms`, `1m`, etc.
