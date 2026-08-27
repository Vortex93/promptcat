import { mkdir } from 'node:fs/promises'
import { $ } from 'zx'

await mkdir('bin', { recursive: true })
await $`go build -o bin/promptcat ./cmd/promptcat`
await $`go build -o bin/promptcat.exe ./cmd/promptcat`
