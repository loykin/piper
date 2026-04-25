package commands

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// mustBindPFlag binds a viper key to a cobra flag.
// Panics immediately on programmer errors such as flag name typos — must be caught at init time.
func mustBindPFlag(key string, flag *pflag.Flag) {
	if flag == nil {
		panic(fmt.Sprintf("mustBindPFlag: flag for key %q is nil (name mismatch?)", key))
	}
	if err := viper.BindPFlag(key, flag); err != nil {
		panic(fmt.Sprintf("mustBindPFlag(%q): %v", key, err))
	}
}
