package piper

import (
	"database/sql"
	"time"
)

// Config는 piper 전체 설정. 구조체로 받아서 임베딩 가능.
type Config struct {
	// 실행
	MaxRetries  int           `yaml:"max_retries"  mapstructure:"max_retries"`
	RetryDelay  time.Duration `yaml:"retry_delay"  mapstructure:"retry_delay"`
	Concurrency int           `yaml:"concurrency"  mapstructure:"concurrency"`
	OutputDir   string        `yaml:"output_dir"   mapstructure:"output_dir"`
	// DB 설정 — 셋 중 하나만 지정. 우선순위: DB > DBDriver+DBDSN > DBPath
	DBPath   string  `yaml:"db_path"   mapstructure:"db_path"`   // sqlite 파일 경로 (기본: output_dir/piper.db)
	DBDriver string  `yaml:"db_driver" mapstructure:"db_driver"` // "postgres" 등 외부 DB 드라이버
	DBDSN    string  `yaml:"db_dsn"    mapstructure:"db_dsn"`    // 외부 DB DSN
	DB       *sql.DB `yaml:"-" mapstructure:"-"`                 // 직접 주입 (*sql.DB)

	// Hooks — 모든 확장 포인트. nil이면 no-op.
	Hooks Hooks `yaml:"-" mapstructure:"-"`

	// Git 소스
	Git GitConfig `yaml:"git" mapstructure:"git"`

	// S3 소스
	S3 S3Config `yaml:"s3" mapstructure:"s3"`

	// 서버 (임베딩 모드에서는 불필요)
	Server ServerConfig `yaml:"server" mapstructure:"server"`
}

type GitConfig struct {
	User  string `yaml:"user"  mapstructure:"user"`
	Token string `yaml:"token" mapstructure:"token"`
}

type S3Config struct {
	Endpoint  string `yaml:"endpoint"   mapstructure:"endpoint"`
	AccessKey string `yaml:"access_key" mapstructure:"access_key"`
	SecretKey string `yaml:"secret_key" mapstructure:"secret_key"`
	Bucket    string `yaml:"bucket"     mapstructure:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"    mapstructure:"use_ssl"`
}

type ServerConfig struct {
	Addr string    `yaml:"addr"    mapstructure:"addr"`
	TLS  TLSConfig `yaml:"tls"     mapstructure:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"   mapstructure:"enabled"`
	CertFile string `yaml:"cert_file" mapstructure:"cert_file"`
	KeyFile  string `yaml:"key_file"  mapstructure:"key_file"`
}

func DefaultConfig() Config {
	return Config{
		MaxRetries:  2,
		RetryDelay:  5 * time.Second,
		Concurrency: 4,
		OutputDir:   "./piper-outputs",
		Server: ServerConfig{
			Addr: ":8080",
		},
	}
}
