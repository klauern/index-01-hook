#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
    printf '%s\n' 'usage: generate-certs.sh OUTPUT_DIR' >&2
    exit 2
fi

output_dir=$1
umask 077
mkdir -p "$output_dir"

openssl genrsa -out "$output_dir/ca.key" 3072 >/dev/null 2>&1
openssl req -x509 -new -sha256 -nodes \
    -key "$output_dir/ca.key" \
    -out "$output_dir/ca.crt" \
    -days 1 \
    -subj '/CN=index-01-hook synthetic E2E CA' \
    -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    >/dev/null 2>&1

openssl genrsa -out "$output_dir/server.key" 2048 >/dev/null 2>&1
openssl req -new -sha256 -nodes \
    -key "$output_dir/server.key" \
    -out "$output_dir/server.csr" \
    -subj '/CN=public.e2e.test' \
    >/dev/null 2>&1
cat >"$output_dir/server.ext" <<'EOF'
authorityKeyIdentifier=keyid,issuer
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
subjectAltName=DNS:api.deepseek.com,DNS:api.ticktick.com,DNS:public.e2e.test
EOF
openssl x509 -req -sha256 \
    -in "$output_dir/server.csr" \
    -CA "$output_dir/ca.crt" \
    -CAkey "$output_dir/ca.key" \
    -CAcreateserial \
    -out "$output_dir/server.crt" \
    -days 1 \
    -extfile "$output_dir/server.ext" \
    >/dev/null 2>&1

openssl req -x509 -newkey rsa:2048 -sha256 -nodes \
    -keyout "$output_dir/wrong-ca.key" \
    -out "$output_dir/wrong-ca.crt" \
    -days 1 \
    -subj '/CN=index-01-hook untrusted synthetic CA' \
    >/dev/null 2>&1
rm -f "$output_dir/ca.key" "$output_dir/wrong-ca.key" \
    "$output_dir/server.csr" "$output_dir/server.ext" "$output_dir/ca.srl"
chmod 0644 "$output_dir/ca.crt" "$output_dir/wrong-ca.crt" "$output_dir/server.crt"
# The key is synthetic and exists only inside the mode-0700 test root. Read
# permission lets two unrelated non-root validation containers mount it.
chmod 0644 "$output_dir/server.key"
