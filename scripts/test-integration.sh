#!/bin/sh
# Real-database tests: MariaDB 10.4 (XAMPP's line) in Docker; the test binary
# runs inside the container, which already has mysqldump and mysql.
set -eu
image=mariadb:10.4
name="sevjala-it-$$"
out=$(mktemp -d)
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$out"' EXIT

docker run -d --name "$name" -e MARIADB_ALLOW_EMPTY_ROOT_PASSWORD=1 "$image" >/dev/null
arch=$(docker version -f '{{.Server.Arch}}')
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -tags integration -o "$out/dump.test" ./dump

i=0
until docker exec "$name" mysqladmin --protocol=TCP -h127.0.0.1 ping >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 60 ] || { docker logs "$name"; exit 1; }
	sleep 1
done
docker cp "$out/dump.test" "$name:/tmp/dump.test"
docker exec -e SEVJALA_TEST_DB=127.0.0.1:3306 "$name" /tmp/dump.test -test.v -test.run Integration
