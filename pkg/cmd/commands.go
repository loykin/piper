// Package cmd는 piper의 cobra 커맨드를 라이브러리로 제공한다.
// data-voyager 등 외부 앱이 자신의 CLI에 piper 커맨드를 추가할 수 있다.
//
//	import pipercmd "github.com/piper/piper/pkg/cmd"
//
//	// voyager CLI에 piper 커맨드 추가
//	rootCmd.AddCommand(pipercmd.Commands(p)...)
//
//	// 결과:
//	// voyager pipeline run train.yaml
//	// voyager pipeline server
//	// voyager pipeline worker --master ...
package cmd

import (
	"github.com/piper/piper/pkg/piper"
	"github.com/spf13/cobra"
)

// Commands piper 인스턴스에 바인딩된 cobra 커맨드 슬라이스를 반환한다.
// 반환된 커맨드를 원하는 parent command에 AddCommand로 추가하면 된다.
func Commands(p *piper.Piper) []*cobra.Command {
	return []*cobra.Command{
		newRunCmd(p),
		newParseCmd(p),
		newServerCmd(p),
		newWorkerCmd(p),
		newAgentCmd(),
	}
}
