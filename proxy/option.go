package proxy

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/constant"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/option"
)

type Option struct {
	path *string
}

func (o *Option) Name() string {
	return Name
}

func detectAndReadConfig(file string) ([]byte, bool, error) {
	switch ext := strings.ToLower(file); {
	case strings.HasSuffix(ext, ".json"):
		data, err := os.ReadFile(file)
		return data, true, err
	case strings.HasSuffix(ext, ".yaml"), strings.HasSuffix(ext, ".yml"):
		data, err := os.ReadFile(file)
		return data, false, err
	default:
		return nil, false, common.NewError("unsupported config format: " + file + ". use .yaml or .json instead")
	}
}

func (o *Option) Handle() error {
	defaultConfigPath := []string{
		"config.json",
		"config.yml",
		"config.yaml",
	}

	var (
		isJSON bool
		data   []byte
		err    error
	)

	switch p := *o.path; p {
	case "":
		log.Warn("no specified config file, searching for default config")
		for _, file := range defaultConfigPath {
			log.Debug("trying config:", file)
			data, isJSON, err = detectAndReadConfig(file)
			if err == nil {
				log.Info("loaded config from:", file)
				break
			}
			if !os.IsNotExist(err) {
				log.Warn("failed to read", file, ":", err)
			}
		}
	default:
		data, isJSON, err = detectAndReadConfig(p)
		if err != nil {
			return common.NewError("failed to read config file: " + p).Base(err)
		}
	}

	if data == nil {
		return common.NewError("no valid config found. tried: " + strings.Join(defaultConfigPath, ", "))
	}

	log.Info("trojan-go", constant.Version, "initializing")
	proxy, err := NewProxyFromConfigData(data, isJSON)
	if err != nil {
		return common.NewError("failed to create proxy").Base(err)
	}
	return proxy.Run()
}

func (o *Option) Priority() int {
	return -1
}

func init() {
	option.RegisterHandler(&Option{
		path: flag.String("config", "", "Trojan-Go config filename (.yaml/.yml/.json)"),
	})
	option.RegisterHandler(&StdinOption{
		format:       flag.String("stdin-format", "disabled", "Read from standard input (yaml/json)"),
		suppressHint: flag.Bool("stdin-suppress-hint", false, "Suppress hint text"),
	})
}

type StdinOption struct {
	format       *string
	suppressHint *bool
}

func (o *StdinOption) Name() string {
	return Name + "_STDIN"
}

func (o *StdinOption) Handle() error {
	isJSON, err := o.isFormatJson()
	if err != nil {
		return err
	}

	if o.suppressHint == nil || !*o.suppressHint {
		fmt.Printf("Trojan-Go %s (%s/%s)\n", constant.Version, runtime.GOOS, runtime.GOARCH)
		if isJSON {
			fmt.Println("Reading JSON configuration from stdin.")
		} else {
			fmt.Println("Reading YAML configuration from stdin.")
		}
	}

	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return common.NewError("failed to read from stdin").Base(err)
	}

	proxy, err := NewProxyFromConfigData(data, isJSON)
	if err != nil {
		return common.NewError("failed to create proxy").Base(err)
	}
	return proxy.Run()
}

func (o *StdinOption) Priority() int {
	return 0
}

func (o *StdinOption) isFormatJson() (bool, error) {
	if o.format == nil {
		return false, common.NewError("format specifier is nil")
	}
	switch strings.ToLower(*o.format) {
	case "disabled":
		return false, common.NewError("reading from stdin is disabled")
	case "json":
		return true, nil
	case "yaml", "yml":
		return false, nil
	default:
		return false, common.NewError("invalid stdin format: " + *o.format + ". use json or yaml")
	}
}
