package mood

import (
	"math/rand"
)

// WeightedEmoji pairs an emoji with a selection weight.
type WeightedEmoji struct {
	Emoji  string
	Weight int
}

// MoodAliases maps behavioral state to a mood alias used in reaction maps.
var MoodAliases = map[string]string{
	"on_fire":   "enthusiastic",
	"flowing":   "helpful",
	"neutral":   "neutral",
	"careful":   "cautious",
	"off_track": "cautious",
}

// MoodProbabilities defines the probability of reacting per state.
var MoodProbabilities = map[string]float64{
	"on_fire":   0.95,
	"flowing":   0.75,
	"neutral":   0.50,
	"careful":   0.20,
	"off_track": 0.10,
}

// ReactionMap defines mirror reactions: user emoji → mood alias → weighted emoji pool.
// All emoji are validated against Telegram's allowed reaction emoji list.
var ReactionMap = map[string]map[string][]WeightedEmoji{
	"👍": {
		"helpful":      {{"✍", 30}, {"🙏", 20}, {"💯", 15}, {"🔥", 15}, {"🎉", 10}, {"👌", 10}},
		"enthusiastic": {{"🔥", 40}, {"💯", 30}, {"🎉", 20}, {"⚡", 10}},
		"sarcastic":    {{"🤡", 20}, {"🤪", 20}, {"👀", 15}, {"🆒", 15}, {"🤷", 15}, {"👌", 15}},
		"cautious":     {{"👍", 40}, {"👌", 30}, {"🙏", 20}, {"🤝", 10}},
		"neutral":      {{"👍", 25}, {"🙏", 20}, {"💯", 15}, {"👌", 10}, {"✍", 30}},
	},
	"👎": {
		"helpful":      {{"🙏", 30}, {"🤔", 20}, {"💔", 15}, {"😢", 10}, {"🤗", 25}},
		"enthusiastic": {{"😢", 30}, {"💔", 25}, {"🙏", 20}, {"😭", 15}, {"🤯", 10}},
		"sarcastic":    {{"🤡", 35}, {"💩", 30}, {"🤷", 20}, {"🤪", 10}, {"🗿", 5}},
		"cautious":     {{"🙏", 40}, {"🤔", 30}, {"😐", 20}, {"👀", 10}},
		"neutral":      {{"🙏", 30}, {"🤔", 25}, {"😐", 20}, {"💔", 15}, {"😢", 10}},
	},
	"❤": {
		"helpful":      {{"🥰", 30}, {"❤", 25}, {"🙏", 20}, {"💘", 15}, {"💋", 10}},
		"enthusiastic": {{"❤\u200d🔥", 40}, {"🥰", 30}, {"🎉", 20}, {"💯", 10}},
		"sarcastic":    {{"🤪", 30}, {"😘", 25}, {"🤡", 20}, {"💘", 15}, {"🥰", 10}},
		"cautious":     {{"🙏", 40}, {"❤", 30}, {"🥰", 20}, {"😇", 10}},
		"neutral":      {{"❤", 30}, {"🥰", 25}, {"🙏", 20}, {"💘", 15}, {"😇", 10}},
	},
	"🔥": {
		"helpful":      {{"💯", 35}, {"🔥", 30}, {"🎉", 20}, {"⚡", 10}, {"🏆", 5}},
		"enthusiastic": {{"🔥", 40}, {"💯", 35}, {"⚡", 15}, {"🏆", 10}},
		"sarcastic":    {{"🤡", 30}, {"🗿", 25}, {"🔥", 20}, {"💩", 15}, {"🤷", 10}},
		"cautious":     {{"👍", 40}, {"👌", 30}, {"🔥", 20}, {"💯", 10}},
		"neutral":      {{"🔥", 30}, {"💯", 25}, {"🎉", 20}, {"👍", 15}, {"⚡", 10}},
	},
	"😂": {
		"helpful":      {{"😁", 30}, {"🤣", 25}, {"😇", 20}, {"🎉", 15}, {"👌", 10}},
		"enthusiastic": {{"🤣", 40}, {"🔥", 20}, {"🎉", 20}, {"💯", 10}, {"😁", 10}},
		"sarcastic":    {{"🤡", 35}, {"😈", 30}, {"🗿", 20}, {"🤣", 10}, {"🤷", 5}},
		"cautious":     {{"😁", 40}, {"👍", 30}, {"🙏", 20}, {"👌", 10}},
		"neutral":      {{"😁", 30}, {"🤣", 25}, {"👌", 20}, {"🎉", 15}, {"😇", 10}},
	},
	"🤔": {
		"helpful":      {{"🤔", 30}, {"🤓", 25}, {"👀", 20}, {"🤷", 15}, {"🧐", 10}},
		"enthusiastic": {{"🤯", 30}, {"🤔", 20}, {"⚡", 15}, {"🔥", 15}, {"💡", 20}},
		"sarcastic":    {{"🤡", 30}, {"🗿", 25}, {"🤷", 20}, {"😐", 15}, {"🥱", 10}},
		"cautious":     {{"🤔", 40}, {"👀", 30}, {"🤓", 20}, {"🙏", 10}},
		"neutral":      {{"🤔", 30}, {"👀", 25}, {"🤓", 20}, {"🤷", 15}, {"🙏", 10}},
	},
	"💩": {
		"helpful":      {{"🙏", 30}, {"😢", 20}, {"💔", 15}, {"🤗", 25}, {"😭", 10}},
		"enthusiastic": {{"😭", 30}, {"💔", 25}, {"😱", 20}, {"🙏", 15}, {"🤯", 10}},
		"sarcastic":    {{"💩", 40}, {"🤡", 30}, {"🗿", 20}, {"😈", 10}},
		"cautious":     {{"🙏", 40}, {"😐", 30}, {"🤔", 20}, {"👀", 10}},
		"neutral":      {{"🙏", 30}, {"😐", 25}, {"💔", 20}, {"😢", 15}, {"🤔", 10}},
	},
	"🎉": {
		"helpful":      {{"🎉", 30}, {"👏", 25}, {"🙏", 20}, {"💯", 15}, {"🏆", 10}},
		"enthusiastic": {{"🔥", 30}, {"🎉", 25}, {"💯", 20}, {"⚡", 15}, {"🏆", 10}},
		"sarcastic":    {{"🤡", 30}, {"🗿", 25}, {"🆒", 20}, {"🎉", 15}, {"🤷", 10}},
		"cautious":     {{"👍", 40}, {"🎉", 30}, {"👌", 20}, {"🙏", 10}},
		"neutral":      {{"🎉", 30}, {"👍", 25}, {"💯", 20}, {"👏", 15}, {"👌", 10}},
	},
}

// DefaultReactions is the fallback pool when user emoji is not in ReactionMap.
var DefaultReactions = map[string][]WeightedEmoji{
	"helpful":      {{"👍", 30}, {"🙏", 25}, {"👌", 20}, {"😇", 15}, {"✍", 10}},
	"enthusiastic": {{"🔥", 35}, {"💯", 30}, {"🎉", 20}, {"⚡", 10}, {"🏆", 5}},
	"sarcastic":    {{"🤡", 30}, {"🗿", 25}, {"🤷", 20}, {"😈", 15}, {"👀", 10}},
	"cautious":     {{"👍", 40}, {"👌", 30}, {"🙏", 20}, {"👀", 10}},
	"neutral":      {{"👍", 30}, {"🙏", 25}, {"👌", 20}, {"😇", 15}, {"✍", 10}},
}

// SpontaneousReactions defines emoji pools for greeting reactions per mood alias.
var SpontaneousReactions = map[string][]WeightedEmoji{
	"helpful":      {{"👏", 40}, {"😁", 30}, {"🙏", 20}, {"🤝", 10}},
	"enthusiastic": {{"🔥", 35}, {"🎉", 30}, {"👏", 20}, {"💯", 15}},
	"sarcastic":    {{"🗿", 30}, {"🤨", 25}, {"😈", 20}, {"🤡", 15}, {"👏", 10}},
	"cautious":     {{"👏", 50}, {"👍", 30}, {"👀", 20}},
	"neutral":      {{"👏", 50}, {"👍", 30}, {"😁", 20}},
}

// Greetings recognized for spontaneous reactions.
var Greetings = map[string]bool{
	"yo": true, "hey": true, "salut": true, "hello": true,
	"hi": true, "bonjour": true, "ciao": true, "sup": true,
	"hola": true, "wesh": true, "yoo": true, "heyy": true,
	"morning": true, "bonsoir": true, "good morning": true,
}

// ShouldReact returns true if Alf should react based on mood probability.
func ShouldReact(moodState string) bool {
	prob, ok := MoodProbabilities[moodState]
	if !ok {
		prob = 0.50
	}
	return rand.Float64() < prob
}

// ChooseMirror selects a mirror emoji based on user emoji and mood state.
// Returns "" if no mapping exists.
func ChooseMirror(userEmoji, moodState string) string {
	alias := MoodAliases[moodState]
	if alias == "" {
		alias = "neutral"
	}

	pool, ok := ReactionMap[userEmoji]
	if !ok {
		// Use default fallback pool.
		candidates, ok := DefaultReactions[alias]
		if !ok {
			candidates = DefaultReactions["neutral"]
		}
		return weightedPick(candidates)
	}

	candidates, ok := pool[alias]
	if !ok {
		// Fallback to neutral within this emoji's map.
		candidates = pool["neutral"]
		if candidates == nil {
			return ""
		}
	}

	return weightedPick(candidates)
}

// ChooseSpontaneous selects a spontaneous reaction emoji for greetings.
func ChooseSpontaneous(moodState string) string {
	alias := MoodAliases[moodState]
	if alias == "" {
		alias = "neutral"
	}

	candidates, ok := SpontaneousReactions[alias]
	if !ok {
		candidates = SpontaneousReactions["neutral"]
	}
	if len(candidates) == 0 {
		return "👏"
	}

	return weightedPick(candidates)
}

func weightedPick(pool []WeightedEmoji) string {
	total := 0
	for _, we := range pool {
		total += we.Weight
	}
	if total == 0 {
		return pool[0].Emoji
	}

	r := rand.Intn(total)
	for _, we := range pool {
		r -= we.Weight
		if r < 0 {
			return we.Emoji
		}
	}
	return pool[len(pool)-1].Emoji
}
