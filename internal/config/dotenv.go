package config

import (
	"bufio"
	"os"
	"strings"
)

// load key=value pairs from a .env file without overriding real env vars
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // file is optional
	}
	defer f.Close()

	// parse line by line
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		val = strings.Trim(strings.TrimSpace(val), `"'`)

		// real environment wins over the file
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
