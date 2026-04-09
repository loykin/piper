package cmd

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// mustBindPFlag는 viper 키와 cobra 플래그를 바인딩한다.
// 플래그 이름 오타 등 프로그래머 오류 시 즉시 패닉 — init 시점에 잡아야 함.
func mustBindPFlag(key string, flag *pflag.Flag) {
	if flag == nil {
		panic(fmt.Sprintf("mustBindPFlag: flag for key %q is nil (name mismatch?)", key))
	}
	if err := viper.BindPFlag(key, flag); err != nil {
		panic(fmt.Sprintf("mustBindPFlag(%q): %v", key, err))
	}
}
