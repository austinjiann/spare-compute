.PHONY: check deploy-check fmt install-macos macos-package macos-package-check pr-check proto proto-check proto-lint race test uninstall-macos vet

BUF_VERSION := v1.72.0

check: fmt proto-check proto-lint vet test race

pr-check: fmt proto-check proto-lint vet test deploy-check

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
	@for script in \
		deploy/vps/bootstrap-ubuntu.sh \
		deploy/vps/coturn-entrypoint.sh \
		deploy/vps/init.sh \
		deploy/vps/turn-credentials.sh \
		deploy/vps/verify.sh; do \
		sh -n "$$script"; \
	done
	@deploy_vps_tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$deploy_vps_tmp"' EXIT; \
	deploy/vps/init.sh --target-dir "$$deploy_vps_tmp" \
		--connectivity-domain connect.example.com \
		--turn-domain turn.example.com \
		--email admin@example.com \
		--public-ip 203.0.113.10 >/dev/null; \
	grep -q '^CONNECTIVITY_DOMAIN=connect.example.com$$' "$$deploy_vps_tmp/.env"; \
	grep -q '^TURN_REALM=turn.example.com$$' "$$deploy_vps_tmp/.env"; \
	test "$$(tr -d '\r\n' < "$$deploy_vps_tmp/secrets/turn_shared_secret" | wc -c | tr -d ' ')" -eq 64; \
	turn_output="$$(deploy/vps/turn-credentials.sh \
		--env-file "$$deploy_vps_tmp/.env" \
		--secret-file "$$deploy_vps_tmp/secrets/turn_shared_secret" \
		--ttl-hours 1 \
		--label deploycheck)"; \
	printf '%s\n' "$$turn_output" | grep -Eq '^Username: [0-9]+:deploycheck$$'; \
	printf '%s\n' "$$turn_output" | grep -q -- 'computehop setup worker --device-name "Gaming PC"'; \
	printf '%s\n' "$$turn_output" | grep -q -- 'turn:turn.example.com:3478?transport=udp'
	cd deploy/vps && TURN_SECRET_FILE=/dev/null docker compose --env-file .env.example config --quiet

macos-package: macos-package-check
	packaging/macos/build.sh

macos-package-check:
	@for script in \
		packaging/macos/build.sh \
		packaging/macos/install.sh \
		packaging/macos/uninstall.sh \
		packaging/macos/verify.sh; do \
		sh -n "$$script"; \
	done
	plutil -lint packaging/macos/Info.plist packaging/macos/com.computehop.daemon.plist

install-macos:
	packaging/macos/install.sh

uninstall-macos:
	packaging/macos/uninstall.sh
