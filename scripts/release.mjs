import { $ } from 'zx'

const version = process.env.VERSION
if (!version || !/^\d+\.\d+\.\d+$/.test(version)) {
  throw new Error('Usage: mise run release VERSION=0.1.0')
}

const tag = `v${version}`
const localTag = await $`git rev-parse --verify refs/tags/${tag}`.nothrow()
if (localTag.exitCode === 0) {
  throw new Error(`Tag ${tag} already exists locally`)
}

const remoteTag = await $`git ls-remote --exit-code --tags origin refs/tags/${tag}`.nothrow()
if (remoteTag.exitCode === 0) {
  throw new Error(`Tag ${tag} already exists on origin`)
}

await $`go test ./...`
await $`git tag ${tag}`
await $`git push origin ${tag}`
