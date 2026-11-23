module drix/server

go 1.26.6

require (
	drix/shared v0.0.0
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/coder/websocket v1.8.15
	github.com/go-webauthn/webauthn v0.18.2
	github.com/jackc/pgx/v5 v5.13.0
	github.com/redis/go-redis/v9 v9.23.0
	golang.org/x/crypto v0.59.0
)

require (
	github.com/fxamacker/cbor/v2 v2.9.4 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.3.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

replace drix/shared => ../shared
