import { describe, expect, it } from 'vitest'

import { isStandaloneEmojiText } from '../lib/chat'

describe('isStandaloneEmojiText', () => {
  it('accepts a single emoji', () => {
    expect(isStandaloneEmojiText('❤️')).toBe(true)
    expect(isStandaloneEmojiText('🔥')).toBe(true)
    expect(isStandaloneEmojiText(' 👍 ')).toBe(true)
  })

  it('accepts multi-codepoint single emoji', () => {
    expect(isStandaloneEmojiText('👍🏽')).toBe(true)
    expect(isStandaloneEmojiText('👨‍👩‍👧‍👦')).toBe(true)
    expect(isStandaloneEmojiText('🇫🇷')).toBe(true)
  })

  it('rejects mixed or multiple visible content', () => {
    expect(isStandaloneEmojiText('❤️❤️')).toBe(false)
    expect(isStandaloneEmojiText('hello ❤️')).toBe(false)
    expect(isStandaloneEmojiText('❤️ merci')).toBe(false)
    expect(isStandaloneEmojiText('')).toBe(false)
  })
})
