package utils

import (
	"os"
	"pg-selector/internal/logger"
	"regexp"
)

// ExpandEnv TODO
func ExpandEnv(input []byte) []byte {
	re := regexp.MustCompile(`\${ENV:([A-Za-z_][A-Za-z0-9_]*)}\$`)
	result := re.ReplaceAllFunc(input, func(match []byte) []byte {
		key := re.FindSubmatch(match)[1]
		if value, exists := os.LookupEnv(string(key)); exists {
			return []byte(value)
		}
		return match
	})

	return result
}

// GetBaseLogExtra TODO
func GetBaseLogExtra(comp string) (logExtra logger.ExtraFieldsT) {
	logExtra = make(logger.ExtraFieldsT)
	logExtra.Set("service", "pg-selector")
	if comp != "none" {
		logExtra.Set("component", comp)
	}

	return logExtra
}
