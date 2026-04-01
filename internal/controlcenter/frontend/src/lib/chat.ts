const EMOJI_CLUSTER_RE = /^(?:[#*0-9]\uFE0F?\u20E3|\p{Regional_Indicator}{2}|\p{Extended_Pictographic}(?:\uFE0F|\uFE0E)?(?:\p{Emoji_Modifier})?(?:\u200D\p{Extended_Pictographic}(?:\uFE0F|\uFE0E)?(?:\p{Emoji_Modifier})?)*)$/u

function splitGraphemes(text: string): string[] {
  if (typeof Intl !== 'undefined' && typeof Intl.Segmenter === 'function') {
    const segmenter = new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    return Array.from(segmenter.segment(text), (part) => part.segment)
  }
  return Array.from(text)
}

export function isStandaloneEmojiText(text: string): boolean {
  const trimmed = text.trim()
  if (!trimmed) return false

  const graphemes = splitGraphemes(trimmed).filter((segment) => segment.trim() !== '')
  if (graphemes.length !== 1) return false

  return EMOJI_CLUSTER_RE.test(graphemes[0])
}
