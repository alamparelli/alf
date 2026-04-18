module github.com/alamparelli/alf

go 1.25.8

require github.com/alessandrolamparelli/vault-proxy v0.2.0

require (
	github.com/asg017/sqlite-vec-go-bindings v0.1.6
	github.com/creack/pty v1.1.24
	github.com/elazarl/goproxy v1.8.2
	github.com/mattn/go-sqlite3 v1.14.34
	github.com/robfig/cron/v3 v3.0.1
	github.com/ti-mo/conntrack v0.6.0
	github.com/ti-mo/netfilter v0.5.3
	github.com/yalue/onnxruntime_go v1.26.0
	golang.org/x/crypto v0.49.0
	golang.org/x/image v0.38.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	nhooyr.io/websocket v1.8.17
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/mdlayher/netlink v1.7.2 // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
)

replace github.com/alessandrolamparelli/vault-proxy => ./internal/controlcenter/frontend/third_party/vault-proxy
