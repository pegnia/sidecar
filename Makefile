REGISTRY   := docker.io
IMAGE_NAME := pseudokouts/agnostic-agones-sidecar

# --- Variables ---
# The name of the binary that will be built inside the Dockerfile
BINARY_NAME := sidecar

# The primary tag for the image. Defaults to 'latest'.
# Can be overridden from the command line, e.g., `make TAG=v1.0.1 build`
TAG         := latest

# Automatically determine a version tag from Git.
# Example: v1.2.3-4-g5c0f8f7 (tag, commits since tag, commit hash)
# If no tags, it will just be the commit hash.
GIT_TAG     := $(shell git describe --tags --always --dirty)

# --- Targets ---
.PHONY: all build push release clean help

# Default target when you just run `make`
all: build
	@echo "Build complete. Image tagged as:"
	@echo "  - $(REGISTRY)/$(IMAGE_NAME):$(TAG)"
	@echo "  - $(REGISTRY)/$(IMAGE_NAME):$(GIT_TAG)"
	@echo "Run 'make push' to push to the registry."

# Build the Docker image with two tags: the primary tag (e.g., 'latest') and the Git version tag.
build:
	@echo "--> Building Docker image..."
	docker build \
		-t $(REGISTRY)/$(IMAGE_NAME):$(TAG) \
		-t $(REGISTRY)/$(IMAGE_NAME):$(GIT_TAG) .

# Push the built images to the container registry.
push:
	@echo "--> Pushing tags to $(REGISTRY)..."
	docker push $(REGISTRY)/$(IMAGE_NAME):$(TAG)
	docker push $(REGISTRY)/$(IMAGE_NAME):$(GIT_TAG)

# A convenience target to build and then immediately push the image.
release: build push
	@echo "--> Release complete!"

# Clean up local build artifacts (if any were created locally)
clean:
	@echo "--> Cleaning up..."
	@if [ -f $(BINARY_NAME) ]; then rm $(BINARY_NAME); fi

# A self-documenting help target
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
	@echo