module drix/agent

go 1.26.6

require (
	drix/shared v0.0.0
	github.com/coder/websocket v1.8.15
	github.com/miekg/dns v1.1.73
	github.com/prometheus-community/pro-bing v0.9.1
	google.golang.org/grpc v1.85.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace drix/shared => ../shared
