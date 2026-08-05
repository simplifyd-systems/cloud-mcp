VERSION ?= 0.0.3
REGISTRY ?= image-hub.simplifyd.dev/cloud
IMAGE ?= $(REGISTRY)/cloud-mcp:$(VERSION)
IMAGE_LATEST ?= $(REGISTRY)/cloud-mcp:latest
PLATFORM ?= linux/amd64
NAMESPACE ?= cloud
DEPLOYMENT ?= cloud-mcp

# Unlike cloudapi, this build needs no netrc or GOPRIVATE args: cloud-go-sdk is
# a public module on the Go proxy, and the build context is this directory
# rather than the monorepo root.

.PHONY: build test lint run
build:
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.serverVersion=$(VERSION)" -o cloud-mcp .

test:
	go test -race ./...

lint:
	go vet ./...
	gofmt -l .

# Run locally over HTTP, as the container does. Stdio mode is what a local MCP
# client launches; there is no point running that from a shell.
run: build
	MCP_TRANSPORT=http MCP_ADDR=:8080 ./cloud-mcp

.PHONY: build_docker_image push_docker_image build_and_push_docker_image build_and_push_docker_image_apple_m1
build_docker_image:
	docker buildx build --platform $(PLATFORM) --load -t cloud-mcp \
		--build-arg VERSION=$(VERSION) -f Dockerfile .

push_docker_image:
	docker tag cloud-mcp $(IMAGE)
	docker tag cloud-mcp $(IMAGE_LATEST)
	docker push $(IMAGE)
	docker push $(IMAGE_LATEST)

# Both tags are pushed: infra/mcp.yaml pins the version, while :latest keeps
# the registry browsable and matches how the other cloud images are tagged.
build_and_push_docker_image:
	docker buildx build --platform $(PLATFORM) --push \
		-t $(IMAGE) -t $(IMAGE_LATEST) \
		--build-arg VERSION=$(VERSION) -f Dockerfile .

build_and_push_docker_image_apple_m1: build_and_push_docker_image

.PHONY: deploy logs status
# Deploys by setting the image rather than deleting a pod.
#
# cloudapi's `kubectl delete pod` works because it runs a single replica; this
# runs two, so deleting one pod would leave the other on the old image. Setting
# the image starts a proper rolling update, and because infra/mcp.yaml pins a
# version tag, it is also what actually moves the deployment to a new version.
deploy:
	kubectl set image deployment/$(DEPLOYMENT) mcp=$(IMAGE) -n $(NAMESPACE)
	kubectl rollout status deployment/$(DEPLOYMENT) -n $(NAMESPACE) --timeout=120s

# Redeploy the same image tag, e.g. after changing env in the manifest.
.PHONY: restart
restart:
	kubectl rollout restart deployment/$(DEPLOYMENT) -n $(NAMESPACE)
	kubectl rollout status deployment/$(DEPLOYMENT) -n $(NAMESPACE) --timeout=120s

logs:
	kubectl logs -n $(NAMESPACE) -l app=$(DEPLOYMENT) -f --tail=50 --max-log-requests=5

status:
	kubectl get pods -n $(NAMESPACE) -l app=$(DEPLOYMENT) -o wide

.PHONY: build_push_docker_image_and_deploy
build_push_docker_image_and_deploy: build_and_push_docker_image deploy
	kubectl logs -n $(NAMESPACE) -l app=$(DEPLOYMENT) -f --tail=50 --max-log-requests=5

.PHONY: apply
apply:
	kubectl apply -f ../infra/mcp.yaml

# Verify a deployed server end to end: the health probe, the OAuth
# protected-resource document, and that an unauthenticated call is challenged
# rather than served.
.PHONY: smoke
smoke: URL ?= https://mcp.simplifyd.com
smoke:
	@echo "== healthz =="
	@curl -fsS -o /dev/null -w "  %{http_code}\n" $(URL)/healthz
	@echo "== protected resource metadata =="
	@curl -fsS $(URL)/.well-known/oauth-protected-resource | head -c 400; echo
	@echo "== unauthenticated /mcp (expect 401 + WWW-Authenticate) =="
	@curl -sS -o /dev/null -D - -X POST $(URL)/mcp \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
		| grep -iE '^(HTTP/|www-authenticate)'

# Local release artifacts (goreleaser); does not tag or publish.
.PHONY: snapshot release
snapshot:
	goreleaser release --snapshot --clean

release:
	goreleaser release --clean

.PHONY: clean
clean:
	rm -f cloud-mcp
	rm -rf dist
