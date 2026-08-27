import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

test('promptcat.exe is a Windows executable', async () => {
	await import('./build.mjs')
	const binary = await readFile('bin/promptcat.exe')

	assert.equal(binary[0], 0x4d)
	assert.equal(binary[1], 0x5a)
})
