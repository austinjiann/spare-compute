.PHONY: check deploy-check fmt install-macos macos-package macos-package-check proto proto-check proto-lint race test uninstall-macos vet

BUF_VERSION := v1.72.0

check: fmt proto-check proto-lint vet test race

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -d . && exit 1)

proto:
	go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION) generate

proto-check:
	@task_proto_tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$task_proto_tmp"' EXIT; \
	cp -R gen/go "$$task_proto_tmp/go"; \
	cp -R gen/swift "$$task_proto_tmp/swift"; \
	$(MAKE) proto; \
	diff -ru "$$task_proto_tmp/go" gen/go; \
	diff -ru "$$task_proto_tmp/swift" gen/swift

proto-lint:
	go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION) lint

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

deploy-check:
	sh -n deploy/vps/bootstrap-ubuntu.sh deploy/vps/coturn-entrypoint.sh deploy/vps/verify.sh
	cd deploy/vps && TURN_SECRET_FILE=/dev/null docker compose --env-file .env.example config --quiet

macos-package: macos-package-check
	packaging/macos/build.sh

macos-package-check:
	sh -n packaging/macos/build.sh packaging/macos/install.sh packaging/macos/uninstall.sh packaging/macos/verify.sh
	plutil -lint packaging/macos/Info.plist packaging/macos/com.computehop.daemon.plist

install-macos:
	packaging/macos/install.sh

uninstall-macos:
	packaging/macos/uninstall.sh
