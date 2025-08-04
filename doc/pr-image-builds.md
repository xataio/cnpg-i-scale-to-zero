# PR Image Build Setup

This document explains how to set up and use the automatic PR image build feature.

## Usage

When a maintainer wants to build images for a PR:

1. **Add the `build-images` label** to the PR
2. The workflow automatically starts and runs tests/linting
3. **Approval required**: Workflow waits for approval in the `pr-image-build` environment
4. **After approval**: Images are built and tagged as `pr-{number}-{sha}`
5. **Auto-comment**: Bot comments on PR with image details and manifest download link

### Image Tags

PR images are tagged with the format: `pr-{number}-{sha}`

For example:

- `ghcr.io/xataio/cnpg-i-scale-to-zero:pr-123-abc1234`
- `ghcr.io/xataio/cnpg-i-scale-to-zero-sidecar:pr-123-abc1234`

### Security Benefits

- **Label-based trigger**: Only maintainers with write access can add labels
- **Approval required**: Images only build after explicit approval
- **No external access**: External contributors cannot trigger image builds
- Images are clearly tagged as PR builds
- Optional wait timer provides additional security buffer
- Full audit trail of who approved what
- **Auto-notification**: PR gets commented with image details

## Testing with PR Images

Once images are built, you can test them by:

1. Download the generated manifest artifact
2. Update your test cluster with the PR-specific images
3. Or manually reference the PR images in your deployments

## Cleanup

PR images are automatically cleaned up when the PR is closed or merged:

- **Images**: All PR-specific images (`pr-{number}-*`) are deleted from the container registry
- **Artifacts**: Manifest artifacts are removed from workflow runs
- **Notification**: Merged PRs get a comment confirming cleanup

This keeps the registry clean and prevents accumulation of test images.
