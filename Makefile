SHELL := /bin/sh

GOCACHE ?= $(CURDIR)/.cache/go-build
export GOCACHE

.PHONY: check check-build check-format check-mod check-vet

check: check-mod check-format check-vet check-build

check-mod:
	@echo "[check-mod] 校验 Go Module"
	@go mod tidy -diff
	@go mod verify

check-format:
	@echo "[check-format] 校验 Go 格式"
	@unformatted="$$(find . -type f -name '*.go' -not -path './.cache/*' -not -path './vendor/*' -exec gofmt -l {} +)"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下文件未通过 gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

check-vet:
	@echo "[check-vet] 执行 go vet"
	@go vet ./...

check-build:
	@echo "[check-build] 执行 Cool 静态检查与应用构建"
	@go run ./cmd/cool build
