// credential-packer is a build-only helper that encrypts mTLS credentials for
// linker injection into the model proxy binary.
package main

import (
	"fmt"
	"os"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/credentialbundle"
)

// main encrypts the three BuildKit-mounted credential files for the linker.
func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: credential-packer CLIENT_CERT CLIENT_KEY GATEWAY_CA")
		os.Exit(2)
	}
	values := make([][]byte, 3)
	for i, path := range os.Args[1:] {
		value, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read credential: %v\n", err)
			os.Exit(1)
		}
		values[i] = value
		defer credentialbundle.Zero(value)
	}
	keyHex, bundleHex, err := credentialbundle.Seal(values[0], values[1], values[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "encrypt credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s %s\n", keyHex, bundleHex)
}
