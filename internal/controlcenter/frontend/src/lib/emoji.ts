const emojiSegmenter =
  typeof Intl !== 'undefined' && 'Segmenter' in Intl
    ? new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    : null

const EMOJI_ONLY_RE = /^[\p{Emoji}\u200D\uFE0E\uFE0F\u20E3]+$/u
const EXTENDED_PICTOGRAPHIC_RE = /\p{Extended_Pictographic}/u
const FLAG_RE = /^[\u{1F1E6}-\u{1F1FF}]{2}$/u
const KEYCAP_RE = /^[#*0-9]\uFE0F?\u20E3$/u

function segmentGraphemes(text: string): string[] {
  if (emojiSegmenter) {
    return Array.from(emojiSegmenter.segment(text), ({ segment }) => segment)
  }
  return Array.from(text)
}

export function isStandaloneEmojiMessage(text: string): boolean {
  const trimmed = text.trim()
  if (!trimmed) return false

  const segments = segmentGraphemes(trimmed)
  if (segments.length !== 1) return false

  const cluster = segments[0]
  if (!EMOJI_ONLY_RE.test(cluster)) return false

  return (
    EXTENDED_PICTOGRAPHIC_RE.test(cluster) ||
    FLAG_RE.test(cluster) ||
    KEYCAP_RE.test(cluster)
  )
}
