.PHONY: check control-center-check control-center-deps deploy-check fmt install-macos install-macos-check launch-local-validation macos-archive macos-archive-smoke macos-notarize macos-package macos-package-check macos-release-archive pr-check proto proto-check proto-lint race release-check release-version-check test uninstall-macos vet worker-archives worker-archives-check

BUF_VERSION := v1.72.0

check: fmt proto-check proto-lint vet test race control-center-check

pr-check: fmt release-version-check proto-check proto-lint vet test control-center-check deploy-check worker-archives-check

release-check: pr-check macos-archive-smoke worker-archives

launch-local-validation: control-center-deps
	scripts/local-launch-validation.sh

release-version-check:
	node scripts/check-release-version.js

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

control-center-deps:
	@test -d apps/control-center/node_modules || npm ci --prefix apps/control-center

control-center-check: control-center-deps
	npm run lint --prefix apps/control-center
	npm test --prefix apps/control-center

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

macos-archive: macos-package-check
	packaging/macos/archive.sh

macos-release-archive: macos-package-check
	packaging/macos/release-archive.sh

macos-notarize: macos-package-check
	packaging/macos/notarize.sh

macos-archive-smoke: macos-package-check
	packaging/macos/smoke.sh

macos-package-check:
	@for script in \
		packaging/macos/archive.sh \
		packaging/macos/build.sh \
		packaging/macos/install.sh \
		packaging/macos/notarize.sh \
		packaging/macos/release-archive.sh \
		packaging/macos/smoke.sh \
		packaging/macos/uninstall.sh \
		packaging/macos/validate-installed.sh \
		packaging/macos/verify.sh; do \
		sh -n "$$script"; \
	done
	node --check packaging/macos/verify-control-center-background.js
	plutil -lint packaging/macos/Info.plist packaging/macos/com.computehop.daemon.plist packaging/macos/entitlements.plist

worker-archives: worker-archives-check
	packaging/workers/archive.sh
	packaging/workers/verify.sh

worker-archives-check:
	@for script in \
		packaging/workers/archive.sh \
		packaging/workers/smoke.sh \
		packaging/workers/verify.sh \
		packaging/workers/linux/run-worker.sh \
		packaging/workers/linux/install-systemd-user.sh \
		packaging/workers/linux/validate-installed-worker.sh; do \
		sh -n "$$script"; \
	done
	packaging/workers/smoke.sh

install-macos:
	packaging/macos/install.sh

install-macos-check:
	packaging/macos/install.sh --check

uninstall-macos:
	packaging/macos/uninstall.sh
