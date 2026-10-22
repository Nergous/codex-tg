import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { createHost } from '../lib/host.js'
import { readPresentedFile, toPcm16k, wavFromPcm } from '../lib/host-utils.js'
import { CHAT, fakeHost, makeBridge, settle, topicMessage } from './harness.mjs'
import { createFakeDsh } from './fake-dsh.mjs'

async function env0(t, options) {
  const env = await makeBridge(options)
  t.after(env.close)
  await env.store.putTopic('session-a', { chatId: CHAT, threadId: 300, name: 'ws · t', closed: false })
  return env
}

const hasFfmpeg = (() => {
  try {
    execFileSync('ffmpeg', ['-version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

test('a photo with caption becomes an image attachment plus text', async (t) => {
  const env = await env0(t)
  env.tg.addFile('photo-big', Buffer.from([0xff, 0xd8, 0xff, 0xe0, 1, 2, 3]))
  await env.bridge.handleUpdate(topicMessage(300, undefined, {
    caption: 'what is on this screenshot?',
    photo: [{ file_id: 'photo-small', file_size: 10 }, { file_id: 'photo-big', file_size: 7 }],
  }))
  const send = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(send.input.text, 'what is on this screenshot?')
  assert.equal(send.input.attachments.length, 1)
  assert.equal(send.input.attachments[0].kind, 'image')
  assert.equal(send.input.attachments[0].mime, 'image/jpeg')
  assert.equal(send.input.attachments[0].data.length, 7)
})

test('a document becomes a file attachment with its name', async (t) => {
  const env = await env0(t)
  env.tg.addFile('doc1', Buffer.from('a,b\n1,2\n'))
  await env.bridge.handleUpdate(topicMessage(300, undefined, { document: { file_id: 'doc1', file_name: 'data.csv', mime_type: 'text/csv', file_size: 8 } }))
  const send = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(send.input.attachments[0].kind, 'file')
  assert.equal(send.input.attachments[0].name, 'data.csv')
  assert.equal(send.input.attachments[0].data.toString(), 'a,b\n1,2\n')
})

test('oversized files are refused before download', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, undefined, { document: { file_id: 'huge', file_name: 'x.bin', file_size: 50 * 1024 * 1024 } }))
  await settle(env)
  assert.equal(env.host.calls.length, 0)
  assert.match(env.tg.callsOf('sendMessage').at(-1).params.text, /larger than 20 MB/)
})

test('a voice note is transcribed, echoed, and sent as text', async (t) => {
  const env = await env0(t)
  env.tg.addFile('voice1', Buffer.from('OggS fake'))
  await env.bridge.handleUpdate(topicMessage(300, undefined, { voice: { file_id: 'voice1', mime_type: 'audio/ogg', duration: 3, file_size: 9 } }))
  await settle(env)
  const send = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(send.input.text, 'привет из голосового')
  assert.equal(send.input.attachments.length, 0)
  assert.ok(env.tg.callsOf('sendMessage').some((call) => /🎙 <i>привет из голосового<\/i>/.test(call.params.text)))
})

test('without transcription the voice note is attached as an audio file', async (t) => {
  const env = await env0(t, { host: fakeHost({ transcribe: async () => undefined }) })
  env.tg.addFile('voice2', Buffer.from('OggS fake'))
  await env.bridge.handleUpdate(topicMessage(300, undefined, { voice: { file_id: 'voice2', mime_type: 'audio/ogg', file_size: 9 } }))
  const send = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(send.input.attachments[0].name.endsWith('.ogg'), true)
})

test('host.sendInput uses the GUI prompt path: image parts inline, files uploaded, tg- request id', async () => {
  const dsh = createFakeDsh()
  dsh.addSession('session-a')
  const uploads = []
  dsh.services.fileUploads = {
    upload: async (agent, file) => {
      uploads.push({ agent: agent.id, name: file.name, size: Buffer.from(file.data, 'base64').length })
      return { receiptId: 'rcpt-1' }
    },
  }
  const host = createHost(dsh.ctx, () => ({}))
  const requestId = await host.sendInput('session-a', {
    text: 'look',
    attachments: [
      { kind: 'image', data: Buffer.from([1, 2, 3]), mime: 'image/jpeg', name: 'p.jpg' },
      { kind: 'file', data: Buffer.from('hello'), mime: 'text/plain', name: 'a.txt' },
    ],
  }, { mode: 'steer' })
  assert.match(requestId, /^tg-/)
  const prompt = dsh.prompts[0]
  assert.equal(prompt.sessionId, 'session-a')
  assert.equal(prompt.mode, 'steer')
  assert.equal(prompt.requestId, requestId)
  assert.deepEqual(prompt.content[0], { type: 'text', text: 'look' })
  assert.deepEqual(prompt.content[1], { type: 'image', mediaType: 'image/jpeg', data: 'AQID', name: 'p.jpg' })
  assert.deepEqual(prompt.content[2], { type: 'file', receiptId: 'rcpt-1' })
  assert.deepEqual(uploads, [{ agent: 'session-a', name: 'a.txt', size: 5 }])
  await host.sendInput('session-a', { text: 'queued', attachments: [] })
  assert.equal(dsh.prompts[1].mode, 'queue')
})

test('wavFromPcm writes the canonical 44-byte header DSH speech-to-text accepts', () => {
  const wav = wavFromPcm(Buffer.alloc(32000))
  assert.equal(wav.length, 44 + 32000)
  assert.equal(wav.toString('ascii', 0, 4), 'RIFF')
  assert.equal(wav.readUInt32LE(4), 36 + 32000)
  assert.equal(wav.toString('ascii', 8, 16), 'WAVEfmt ')
  assert.equal(wav.readUInt32LE(16), 16)
  assert.equal(wav.readUInt16LE(20), 1)
  assert.equal(wav.readUInt16LE(22), 1)
  assert.equal(wav.readUInt32LE(24), 16000)
  assert.equal(wav.readUInt32LE(28), 32000)
  assert.equal(wav.readUInt16LE(32), 2)
  assert.equal(wav.readUInt16LE(34), 16)
  assert.equal(wav.toString('ascii', 36, 40), 'data')
  assert.equal(wav.readUInt32LE(40), 32000)
})

test('ffmpeg converts an OGG/Opus voice note to 16 kHz mono PCM', { skip: hasFfmpeg ? false : 'ffmpeg not installed' }, async () => {
  const dir = mkdtempSync(join(tmpdir(), 'tg-voice-test-'))
  try {
    const file = join(dir, 'tone.ogg')
    execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=1', '-c:a', 'libopus', '-ar', '48000', file])
    const { readFileSync } = await import('node:fs')
    const pcm = await toPcm16k(readFileSync(file))
    // one second of 16 kHz 16-bit mono, allowing codec padding
    assert.ok(pcm.length > 28000 && pcm.length < 36000, `pcm length ${pcm.length}`)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('host.transcribe uses DSH speech-to-text when loaded', { skip: hasFfmpeg ? false : 'ffmpeg not installed' }, async () => {
  const dsh = createFakeDsh()
  let received
  dsh.services.speechToText = {
    resolve: (request) => request,
    transcribe: async (spec) => {
      received = spec
      return { text: ' hello there ' }
    },
  }
  const dir = mkdtempSync(join(tmpdir(), 'tg-voice-test-'))
  try {
    const file = join(dir, 'tone.ogg')
    execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=0.5', '-c:a', 'libopus', file])
    const { readFileSync } = await import('node:fs')
    const host = createHost(dsh.ctx, () => ({ ffmpegPath: 'ffmpeg' }))
    const text = await host.transcribe(readFileSync(file), 'audio/ogg')
    assert.equal(text, 'hello there')
    assert.equal(Buffer.from(received.audio).toString('ascii', 0, 4), 'RIFF')
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('host.transcribe returns undefined when no transcriber is configured', async () => {
  const dsh = createFakeDsh()
  const host = createHost(dsh.ctx, () => ({}))
  assert.equal(await host.transcribe(Buffer.from('x')), undefined)
})

test('readPresentedFile resolves relative paths against the session cwd', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'tg-present-'))
  try {
    writeFileSync(join(dir, 'out.txt'), 'data')
    const blob = await readPresentedFile('out.txt', dir)
    assert.equal(blob.name, 'out.txt')
    assert.equal(await blob.text(), 'data')
    await assert.rejects(readPresentedFile('missing.txt', dir))
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
