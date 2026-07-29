package main

import (
	"flag"
	"log/slog"
	"os"

	ixr "github.com/YashVishwas/ixr/pkg/ixr"
)

// generatedConfigPath is where a config supplied via IXR_CONFIG_YAML is
// written before being loaded. A fixed path under "/" rather than a
// TempDir/TempFile-based one: the release Dockerfile's final stage is
// FROM scratch, which has no /tmp directory, but the container's root is
// always writable at runtime.
const generatedConfigPath = "/ixr-runtime-config.yaml"

func main() {
	configFile := flag.String("config", "", "path to ixr.yaml (auto-discovered if not set)")
	port := flag.Int("port", 0, "listen port (overrides config file; default 7000)")
	flag.Parse()

	// Some deployment targets (e.g. Railway) make it easy to set an
	// environment variable but awkward to ship or mount a config file.
	// IXR_CONFIG_YAML lets the whole ixr.yaml live as one env var instead —
	// only consulted when -config wasn't passed explicitly, so it never
	// overrides an operator's explicit choice.
	if *configFile == "" {
		if yamlContent := os.Getenv("IXR_CONFIG_YAML"); yamlContent != "" {
			if err := os.WriteFile(generatedConfigPath, []byte(yamlContent), 0o600); err != nil {
				slog.Error("failed to write IXR_CONFIG_YAML to disk", "err", err)
				os.Exit(1)
			}
			*configFile = generatedConfigPath
		}
	}

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
