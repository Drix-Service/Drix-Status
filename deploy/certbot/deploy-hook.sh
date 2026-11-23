#!/bin/sh
# Copies the (re)issued certificate to Envoy's watched directory. Files are
# written then moved into place, which makes Envoy reload them.
set -eu
src="${RENEWED_LINEAGE:-/etc/letsencrypt/live/drix}"
dst=/etc/envoy/tls
cp "$src/fullchain.pem" "$dst/.fullchain.pem.new"
cp "$src/privkey.pem" "$dst/.privkey.pem.new"
# Envoy runs as uid 101 (ENVOY_UID in docker-compose.tls.yml) and must read the key.
chown 101:101 "$dst/.privkey.pem.new"
chmod 600 "$dst/.privkey.pem.new"
mv -f "$dst/.fullchain.pem.new" "$dst/fullchain.pem"
mv -f "$dst/.privkey.pem.new" "$dst/privkey.pem"
cp "$dst/sds.yaml" "$dst/.sds.yaml.new" && mv -f "$dst/.sds.yaml.new" "$dst/sds.yaml"
echo "Installed certificate from $src"
