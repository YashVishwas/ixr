package main

import (
	"flag"
	"log/slog"
	"os"

	ixr "github.com/YashVishwas/ixr/pkg/ixr"
)

func main() {
	configFile := flag.String("config", "", "path to ixr.yaml (auto-discovered if not set)")
	port := flag.Int("port", 0, "listen port (overrides config file; default 7000)")
	flag.Parse()

	var opts []ixr.Option
	if *configFile != "" {
		opts = append(opts, ixr.WithConfigFile(*configFile))
	}
	if *port != 0 {
		opts = append(opts, ixr.WithPort(*port))
	}

	if err := ixr.Start(opts...); err != nil {
		slog.Error("ixr exited", "err", err)
		os.Exit(1)
	}
}
