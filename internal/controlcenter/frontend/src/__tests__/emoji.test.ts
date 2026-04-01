import { describe, expect, it } from 'vitest'

import { isStandaloneEmojiMessage } from '../lib/emoji'

describe('isStandaloneEmojiMessage()', () => {
  it('accepts a single emoji', () => {
    expect(isStandaloneEmojiMessage('❤️')).toBe(true)
    expect(isStandaloneEmojiMessage('🔥')).toBe(true)
    expect(isStandaloneEmojiMessage(' ❤️ ')).toBe(true)
  })

  it('rejects multiple emoji clusters', () => {
    expect(isStandaloneEmojiMessage('❤️❤️')).toBe(false)
    expect(isStandaloneEmojiMessage('🔥 😂')).toBe(false)
  })

  it('rejects mixed text content', () => {
    expect(isStandaloneEmojiMessage('hello ❤️')).toBe(false)
    expect(isStandaloneEmojiMessage('ok ❤️')).toBe(false)
  })

  it('handles multi-codepoint emoji correctly', () => {
    expect(isStandaloneEmojiMessage('👍🏽')).toBe(true)
    expect(isStandaloneEmojiMessage('👨‍💻')).toBe(true)
    expect(isStandaloneEmojiMessage('🇧🇪')).toBe(true)
    expect(isStandaloneEmojiMessage('#️⃣')).toBe(true)
  })

  it('does not treat plain digits as emoji messages', () => {
    expect(isStandaloneEmojiMessage('1')).toBe(false)
  })
})
