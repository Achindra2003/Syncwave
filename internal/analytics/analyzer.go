package analytics

import (
	"math"
	"regexp"
	"strings"
)

// AnalysisResult holds text metrics.
type AnalysisResult struct {
	WordCount     int
	SentenceCount int
	SyllableCount int
	ReadingScore  float64 // Flesch Reading Ease
}

// AnalyzeText performs string parsing and mathematical computation on text.
func AnalyzeText(text string) AnalysisResult {
	if strings.TrimSpace(text) == "" {
		return AnalysisResult{}
	}

	// 1. String Manipulation: Count Words
	words := strings.Fields(text)
	wordCount := len(words)

	// 2. String Manipulation: Parse Sentences (roughly split on . ? !)
	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	sentences := sentenceRegex.Split(text, -1)
	sentenceCount := 0
	for _, s := range sentences {
		if strings.TrimSpace(s) != "" {
			sentenceCount++
		}
	}
	if sentenceCount == 0 {
		sentenceCount = 1 // Prevent division by zero
	}

	// 3. String Manipulation: Parse Syllables (rough estimation via vowels)
	vowelRegex := regexp.MustCompile(`(?i)[aeiouy]+`)
	syllableCount := 0
	for _, word := range words {
		cleaned := strings.TrimRight(word, "eE.,!?\"'") // remove silent e's and punctuation
		matches := vowelRegex.FindAllStringIndex(cleaned, -1)
		count := len(matches)
		if count == 0 {
			count = 1 // every word has at least 1 syllable
		}
		syllableCount += count
	}

	// 4. Mathematical Computation: Flesch Reading Ease
	// Equation: 206.835 - 1.015 * (total words / total sentences) - 84.6 * (total syllables / total words)
	avgWordsPerSentence := float64(wordCount) / float64(sentenceCount)
	avgSyllablesPerWord := float64(syllableCount) / float64(wordCount)

	rawScore := 206.835 - (1.015 * avgWordsPerSentence) - (84.6 * avgSyllablesPerWord)

	// Round to 2 decimal places using math package
	roundedScore := math.Round(rawScore*100) / 100

	return AnalysisResult{
		WordCount:     wordCount,
		SentenceCount: sentenceCount,
		SyllableCount: syllableCount,
		ReadingScore:  roundedScore,
	}
}
