import { existsSync } from 'node:fs'
import { mkdir } from 'node:fs/promises'
import { $ } from 'zx'

await mkdir('bin', { recursive: true })
const pgo = existsSync('default.pgo') ? '-pgo=default.pgo' : '-pgo=off'
await $`go build ${pgo} -o bin/promptcat ./cmd/promptcat`
await $`go build ${pgo} -o bin/promptcat.exe ./cmd/promptcat`
