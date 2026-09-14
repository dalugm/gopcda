package main

import (
	"fmt"
	"time"

	opcda "github.com/dalugm/gopcda"
)

func loadConfig(
	command string,
	getenv func(string) string,
) (opcda.ServerConfig, time.Duration, error) {
	cfg := opcda.ServerConfig{
		Host:     getenv("OPCDA_HOST"),
		Domain:   getenv("OPCDA_DOMAIN"),
		Username: getenv("OPCDA_USERNAME"),
		Password: getenv("OPCDA_PASSWORD"),
		CLSID:    getenv("OPCDA_CLSID"),
		ProgID:   getenv("OPCDA_PROGID"),
	}
	if command == "resolve" {
		cfg.CLSID, cfg.ProgID = "", ""
		if cfg.Host == "" {
			return cfg, 0, fmt.Errorf(
				"set OPCDA_HOST; credentials use OPCDA_DOMAIN, OPCDA_USERNAME and OPCDA_PASSWORD",
			)
		}
	} else if cfg.Host == "" || (cfg.CLSID == "" && cfg.ProgID == "") {
		return cfg, 0, fmt.Errorf(
			"set OPCDA_HOST and either OPCDA_CLSID or OPCDA_PROGID; credentials use OPCDA_DOMAIN, OPCDA_USERNAME and OPCDA_PASSWORD",
		)
	}
	timeout := 180 * time.Second
	if value := getenv("OPCDA_TIMEOUT"); value != "" {
		var err error
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return cfg, 0, fmt.Errorf("OPCDA_TIMEOUT must be a positive duration such as 180s")
		}
	}
	return cfg, timeout, nil
}
