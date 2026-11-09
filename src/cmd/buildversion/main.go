// buildversion emits an ordered UTC build number with microsecond precision.
package main

import (
	"fmt"
	"strings"
	"time"
)

func main() {
	fmt.Print(strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000"), ".", ""))
}
