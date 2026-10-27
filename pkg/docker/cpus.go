package docker

import (
	"math"
	"strconv"
)

// NanoCPUs converts cpus, as compose holds them, to the billionths of a CPU
// the daemon takes. A float32 holds 0.1 as 0.100000001490116, so the value
// goes through its shortest decimal form, the one the compose file wrote.
func NanoCPUs(cpus float32) int64 {
	exact, _ := strconv.ParseFloat(strconv.FormatFloat(float64(cpus), 'f', -1, 32), 64)
	return int64(math.Round(exact * 1e9))
}
