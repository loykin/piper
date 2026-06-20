package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Loader struct {
	v       *viper.Viper
	mu      sync.Mutex
	loadMu  sync.Mutex
	path    string
	loaded  bool
	readErr error
	cached  *RootConfig
	loadErr error
	flags   map[string]*pflag.Flag
}

func NewLoader() *Loader {
	v := viper.New()
	v.SetEnvPrefix("PIPER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	l := &Loader{v: v, flags: make(map[string]*pflag.Flag)}
	registerDefaults(v, reflect.ValueOf(Defaults()), "")
	for _, key := range schemaKeys(reflect.TypeOf(RootConfig{}), "") {
		_ = v.BindEnv(key)
	}
	return l
}

func (l *Loader) SetConfigFile(path string) { l.path = path }

func (l *Loader) BindFlag(key string, flag *pflag.Flag) error {
	if flag == nil {
		return fmt.Errorf("config: flag for %s is nil", key)
	}
	if err := l.v.BindPFlag(key, flag); err != nil {
		return err
	}
	l.flags[key] = flag
	return nil
}

func (l *Loader) MustBindFlag(key string, flag *pflag.Flag) {
	if err := l.BindFlag(key, flag); err != nil {
		panic(err)
	}
}

func (l *Loader) Read() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded {
		return l.readErr
	}
	l.loaded = true
	if l.path != "" {
		if err := strictFile(l.path); err != nil {
			l.readErr = err
			return err
		}
		l.v.SetConfigFile(l.path)
		if err := l.v.ReadInConfig(); err != nil {
			l.readErr = fmt.Errorf("config: %w", err)
			return l.readErr
		}
		return nil
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		l.v.AddConfigPath(home)
	}
	l.v.AddConfigPath(".")
	l.v.SetConfigName(".piper")
	l.v.SetConfigType("yaml")
	if err := l.v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			l.readErr = fmt.Errorf("config: %w", err)
			return l.readErr
		}
		return nil
	}
	if err := strictFile(l.v.ConfigFileUsed()); err != nil {
		l.readErr = err
		return err
	}
	return nil
}

func (l *Loader) Load() (RootConfig, error) {
	l.loadMu.Lock()
	defer l.loadMu.Unlock()
	if l.cached != nil || l.loadErr != nil {
		if l.cached == nil {
			return RootConfig{}, l.loadErr
		}
		return *l.cached, l.loadErr
	}
	if err := l.Read(); err != nil {
		l.loadErr = err
		return RootConfig{}, err
	}
	cfg := Defaults()
	err := l.v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(), jsonHook(),
	)))
	if err != nil {
		l.loadErr = fmt.Errorf("config: %w", err)
		return RootConfig{}, l.loadErr
	}
	if cfg.Version != 2 {
		l.loadErr = fmt.Errorf("config: version must be 2")
		return RootConfig{}, l.loadErr
	}
	l.cached = &cfg
	return cfg, nil
}

func (l *Loader) ConfigFileUsed() string { return l.v.ConfigFileUsed() }

func (l *Loader) Sources() map[string]string {
	out := make(map[string]string)
	for _, key := range schemaKeys(reflect.TypeOf(RootConfig{}), "") {
		if flag := l.flags[key]; flag != nil && flag.Changed {
			out[key] = "flag"
			continue
		}
		envKey := "PIPER_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		if _, ok := os.LookupEnv(envKey); ok {
			out[key] = "environment"
			continue
		}
		if l.v.InConfig(key) {
			out[key] = "config"
			continue
		}
		out[key] = "default"
	}
	return out
}

func strictFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if len(doc.Content) > 0 {
		if err := validateYAMLNode(doc.Content[0], reflect.TypeOf(RootConfig{}), ""); err != nil {
			return fmt.Errorf("config file %s: %w", path, err)
		}
	}
	var cfg RootConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if cfg.Version != 2 {
		return fmt.Errorf("config file %s: version must be 2", path)
	}
	return nil
}

func validateYAMLNode(node *yaml.Node, typ reflect.Type, prefix string) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(time.Duration(0)) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return nil
		}
		fields := make(map[string]reflect.Type, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("yaml"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			name := node.Content[i].Value
			childType, ok := fields[name]
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if !ok {
				return fmt.Errorf("unknown key %s", path)
			}
			if err := validateYAMLNode(node.Content[i+1], childType, path); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				if err := validateYAMLNode(child, typ.Elem(), prefix); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		return nil
	default:
		return nil
	}
	return nil
}

func jsonHook() mapstructure.DecodeHookFuncType {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || (to.Kind() != reflect.Slice && to.Kind() != reflect.Map) {
			return data, nil
		}
		if strings.TrimSpace(data.(string)) == "" {
			return reflect.Zero(to).Interface(), nil
		}
		out := reflect.New(to)
		if err := json.Unmarshal([]byte(data.(string)), out.Interface()); err != nil {
			return nil, err
		}
		return out.Elem().Interface(), nil
	}
}

func registerDefaults(v *viper.Viper, value reflect.Value, prefix string) {
	t := value.Type()
	for i := 0; i < value.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("mapstructure"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		fv := value.Field(i)
		if fv.Type() != reflect.TypeOf(time.Duration(0)) && fv.Kind() == reflect.Struct {
			registerDefaults(v, fv, key)
			continue
		}
		v.SetDefault(key, fv.Interface())
	}
}

func schemaKeys(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("mapstructure"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		ft := f.Type
		if ft == reflect.TypeOf(time.Duration(0)) || ft.Kind() != reflect.Struct {
			out = append(out, key)
			continue
		}
		out = append(out, schemaKeys(ft, key)...)
	}
	return out
}
