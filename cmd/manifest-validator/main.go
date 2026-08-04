package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/NeKiro-project/NeKiro-Stack/internal/manifest"
)

func main() {
	format := flag.String("format", "summary", "output format: summary or tsv")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: manifest-validator [-format summary|tsv] <components.json>")
		os.Exit(2)
	}
	value, err := manifest.LoadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch *format {
	case "summary":
		fmt.Printf("validated schema %s with %d immutable components\n", value.SchemaVersion, len(value.Ordered()))
	case "tsv":
		for _, item := range value.Ordered() {
			fmt.Printf("%s\t%s\t%s\t%s\n", item.Name, item.Component.Repository, item.Component.CommitSHA, item.Component.Tag)
		}
	default:
		fmt.Fprintln(os.Stderr, "format must be summary or tsv")
		os.Exit(2)
	}
}
