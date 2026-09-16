package main

import (
	"fmt"
	"os"

	"lnmp-panel/internal/app"
)

func main() {
	if err := app.CLI(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}
