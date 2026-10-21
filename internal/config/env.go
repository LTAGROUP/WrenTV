package config

import "os"

const configFile = "wrentv.yaml"

// Getenv returns WRENTV_<name>.
func Getenv(name string) string {
	return os.Getenv("WRENTV_" + name)
}

// LookupEnv is Getenv that also reports whether the variable is set.
func LookupEnv(name string) (string, bool) {
	return os.LookupEnv("WRENTV_" + name)
}
