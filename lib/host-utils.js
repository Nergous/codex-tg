/**
 * Host-side helpers that touch the local machine: git diff for /diff, audio
 * conversion for voice transcription, reading presented files.
 */
import { execFile, spawn } from 'node:child_process'
import { readFile, stat } from 'node:fs/promises'
import { basename, isAbsolute, resolve } from 'node:path'

function run(command, args, { cwd, input, timeoutMs = 30000, maxBuffer = 16 * 1024 * 1024, encoding = 'utf8' } = {}) {
  return new Promise((resolvePromise, reject) => {
    const child = execFile(command, args, { cwd, timeout: timeoutMs, maxBuffer, encoding, env: { ...process.env, GIT_TERMINAL_PROMPT: '0', GIT_OPTIONAL_LOCKS: '0' } }, (error, stdout, stderr) => {
      if (error) {
        error.stderr = String(stderr ?? '')
        reject(error)
        return
      }
      resolvePromise(stdout)
    })
    if (input !== undefined) {
      child.stdin.end(input)
    }
  })
}

/**
 * Uncommitted changes of the repository at `cwd`: a stat summary and the
 * full patch, including untracked files listed by name.
 * @returns {Promise<{ stat: string, patch: string } | undefined>} undefined outside a Git repository.
 */
export async function gitDiff(cwd) {
  if (!cwd) return undefined
  const git = (args) => run('git', ['-c', 'core.fsmonitor=false', '-c', 'core.quotepath=false', '-C', cwd, ...args], { timeoutMs: 20000 })
  try {
    await git(['rev-parse', '--is-inside-work-tree'])
  } catch {
    return undefined
  }
  const [statText, patch, untracked] = await Promise.all([
    git(['diff', 'HEAD', '--stat', '--no-color']).catch(() => git(['diff', '--stat', '--no-color'])),
    git(['diff', 'HEAD', '--no-color']).catch(() => git(['diff', '--no-color'])),
    git(['ls-files', '--others', '--exclude-standard']),
  ])
  const extra = untracked.trim() ? `\nUntracked:\n${untracked.trim().split('\n').map((line) => `  ${line}`).join('\n')}` : ''
  return { stat: `${statText.trim()}${extra}`.trim(), patch }
}

/** Canonical 44-byte PCM WAV header (mono, 16 kHz, 16-bit) as DSH speech-to-text requires. */
export function wavFromPcm(pcm, sampleRate = 16000) {
  const header = Buffer.alloc(44)
  header.write('RIFF', 0, 'ascii')
  header.writeUInt32LE(36 + pcm.length, 4)
  header.write('WAVE', 8, 'ascii')
  header.write('fmt ', 12, 'ascii')
  header.writeUInt32LE(16, 16)
  header.writeUInt16LE(1, 20)
  header.writeUInt16LE(1, 22)
  header.writeUInt32LE(sampleRate, 24)
  header.writeUInt32LE(sampleRate * 2, 28)
  header.writeUInt16LE(2, 32)
  header.writeUInt16LE(16, 34)
  header.write('data', 36, 'ascii')
  header.writeUInt32LE(pcm.length, 40)
  return Buffer.concat([header, pcm])
}

/** Decode any audio container (OGG/Opus voice notes) to 16 kHz mono s16le PCM with ffmpeg. */
export function toPcm16k(data, { ffmpeg = 'ffmpeg', timeoutMs = 60000 } = {}) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(ffmpeg, ['-hide_banner', '-loglevel', 'error', '-i', 'pipe:0', '-ac', '1', '-ar', '16000', '-f', 's16le', 'pipe:1'], { stdio: ['pipe', 'pipe', 'pipe'] })
    const out = []
    const err = []
    const timer = setTimeout(() => child.kill('SIGKILL'), timeoutMs)
    child.stdout.on('data', (chunk) => out.push(chunk))
    child.stderr.on('data', (chunk) => err.push(chunk))
    child.on('error', (error) => {
      clearTimeout(timer)
      reject(error)
    })
    child.on('close', (code) => {
      clearTimeout(timer)
      if (code === 0) resolvePromise(Buffer.concat(out))
      else reject(new Error(`ffmpeg exited with ${code}: ${Buffer.concat(err).toString('utf8').trim().slice(0, 300)}`))
    })
    child.stdin.on('error', () => {})
    child.stdin.end(data)
  })
}

/**
 * Run a user-configured transcription command. `{file}` in the template is
 * replaced by a temporary WAV path; the command prints the transcript.
 */
export async function runTranscribeCommand(template, wav, { timeoutMs = 120000 } = {}) {
  const { mkdtemp, writeFile, rm } = await import('node:fs/promises')
  const { tmpdir } = await import('node:os')
  const { join } = await import('node:path')
  const dir = await mkdtemp(join(tmpdir(), 'dsh-tg-voice-'))
  const file = join(dir, 'voice.wav')
  try {
    await writeFile(file, wav)
    const command = template.includes('{file}') ? template.split('{file}').join(JSON.stringify(file)) : `${template} ${JSON.stringify(file)}`
    const output = await run('/bin/sh', ['-c', command], { timeoutMs })
    return output.trim()
  } finally {
    await rm(dir, { recursive: true, force: true })
  }
}

/** Read a file the agent presented, as a named Blob for sendDocument. */
export async function readPresentedFile(path, cwd, { maxBytes = 50 * 1024 * 1024 } = {}) {
  const full = isAbsolute(path) ? path : resolve(cwd ?? process.cwd(), path)
  const info = await stat(full)
  if (!info.isFile()) throw new Error('not a regular file')
  if (info.size > maxBytes) throw new Error(`file is larger than ${Math.round(maxBytes / 1048576)} MB`)
  const data = await readFile(full)
  const blob = new Blob([data])
  return Object.assign(blob, { name: basename(full) })
}
