#!/usr/bin/env node
/**
 * Repository check: every source file parses, no Go leftovers, required
 * plugin files exist, the manifest is consistent, and the full test suite
 * passes. Prints "repo check passed" only when everything holds.
 */
import { spawnSync } from 'node:child_process'
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const problems = []
const skip = new Set(['.git', 'node_modules', '.scratch', '.unlazy', 'dist'])

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (skip.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) walk(full, out)
    else out.push(full)
  }
  return out
}

const files = walk(root)
const rel = (file) => relative(root, file)

for (const file of files) {
  if (/\.go$|^go\.(mod|sum)$/.test(rel(file))) problems.push(`Go leftover: ${rel(file)}`)
}

for (const file of files.filter((f) => /\.(m?js)$/.test(f))) {
  const result = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' })
  if (result.status !== 0) problems.push(`syntax error in ${rel(file)}: ${result.stderr.trim().split('\n')[0]}`)
}

for (const required of ['package.json', 'index.js', 'client.js', 'cordis.patch.yml', 'icon.svg', 'locale/en.json', 'README.md', 'README.ru.md', 'LICENSE', 'CHANGELOG.md']) {
  if (!existsSync(join(root, required))) problems.push(`missing ${required}`)
}

const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'))
if (pkg.type !== 'module') problems.push('package.json: type must be module')
if (pkg.dependencies && Object.keys(pkg.dependencies).length) problems.push('package.json: a linked DSH plugin must have no runtime dependencies')
const patch = readFileSync(join(root, 'cordis.patch.yml'), 'utf8')
if (!patch.includes(`name: '${pkg.name}'`)) problems.push('cordis.patch.yml row name must equal the package name')
const client = readFileSync(join(root, 'client.js'), 'utf8')
if (!client.includes(`id: '${pkg.name}'`)) problems.push('client.js module id must equal the package name')
for (const locale of readdirSync(join(root, 'locale'))) {
  const data = JSON.parse(readFileSync(join(root, 'locale', locale), 'utf8'))
  if (!data.meta?.title || !data.meta?.description) problems.push(`locale/${locale}: meta.title and meta.description required`)
}
const readme = readFileSync(join(root, 'README.md'), 'utf8')
for (const topic of ['BotFather', '/bind', 'Plugins', 'Topics']) if (!readme.includes(topic)) problems.push(`README.md does not mention ${topic}`)

const tests = readdirSync(join(root, 'test')).filter((name) => name.endsWith('.test.mjs')).map((name) => join('test', name))
const run = spawnSync(process.execPath, ['--test', '--test-reporter=tap', ...tests], { cwd: root, encoding: 'utf8', timeout: 600000 })
const failed = /# fail (\d+)/.exec(run.stdout)?.[1]
const passed = /# pass (\d+)/.exec(run.stdout)?.[1]
if (run.status !== 0 || failed !== '0') {
  problems.push(`tests failed (exit ${run.status}, fail ${failed ?? '?'})`)
  process.stdout.write(run.stdout.split('\n').filter((line) => /^not ok|# fail|error:/.test(line.trim())).join('\n') + '\n')
}

if (problems.length) {
  console.log(problems.map((problem) => `- ${problem}`).join('\n'))
  process.exit(1)
}
console.log(`${tests.length} test files, ${passed} tests passed`)
console.log('repo check passed')
