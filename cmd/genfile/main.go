// genfile creates an explicitly requested deterministic, incompressible-ish
// input file for the baseline. It never overwrites an existing file unless
// -force is passed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	path := flag.String("output", "", "new output file")
	size := flag.Int64("size", 0, "file size in bytes")
	seed := flag.Uint64("seed", 1, "deterministic content seed")
	force := flag.Bool("force", false, "allow truncating an existing file")
	flag.Parse()
	if *path == "" || *size <= 0 {
		fatal(errors.New("output and positive size are required"))
	}
	if _, err := os.Lstat(*path); err == nil && !*force {
		fatal(fmt.Errorf("%s exists; refusing to overwrite without -force", *path))
	} else if err != nil && !os.IsNotExist(err) {
		fatal(err)
	}
	f, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	b := make([]byte, 1<<20)
	var written int64
	state := *seed
	for written < *size {
		for i := range b {
			state ^= state >> 12
			state ^= state << 25
			state ^= state >> 27
			b[i] = byte(state * 2685821657736338717)
		}
		n := int64(len(b))
		if n > *size-written {
			n = *size - written
		}
		if _, err := f.Write(b[:n]); err != nil {
			fatal(err)
		}
		written += n
	}
	if err := f.Sync(); err != nil {
		fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %d bytes to %s (seed=%d)\n", written, *path, *seed)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "genfile:", err); os.Exit(1) }
