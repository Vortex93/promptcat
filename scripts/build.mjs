import { existsSync } from 'node:fs'
import { mkdir } from 'node:fs/promises'
import { $ } from 'zx'

await mkdir('bin', { recursive: true })
const pgo = existsSync('default.pgo') ? '-pgo=default.pgo' : '-pgo=off'
const hostIsWindows = process.platform === 'win32'
const nativeOutput = hostIsWindows ? 'bin/promptcat.exe' : 'bin/promptcat'
const crossTarget = hostIsWindows
	? { goos: 'linux', goarch: 'amd64', output: 'bin/promptcat' }
	: { goos: 'windows', goarch: 'amd64', output: 'bin/promptcat.exe' }

await $`go build ${pgo} -o ${nativeOutput} ./cmd/promptcat`
await $({
	env: {
		...process.env,
		GOOS: crossTarget.goos,
		GOARCH: crossTarget.goarch,
		CGO_ENABLED: '0',
	},
})`go build ${pgo} -o ${crossTarget.output} ./cmd/promptcat`
