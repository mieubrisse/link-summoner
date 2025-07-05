package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type LinkInfo struct {
	Text        string
	Description string
	URL         string
	Confidence  float64
	Sentence    string
	StartPos    int
	EndPos      int
	Settled     bool
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-i] <input-file> [output-file]\n", os.Args[0])
		os.Exit(1)
	}

	inPlace := false
	inputFile := ""
	outputFile := ""

	if os.Args[1] == "-i" {
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s -i <input-file>\n", os.Args[0])
			os.Exit(1)
		}
		inPlace = true
		inputFile = os.Args[2]
	} else {
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s <input-file> <output-file>\n", os.Args[0])
			os.Exit(1)
		}
		inputFile = os.Args[1]
		outputFile = os.Args[2]
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: OPENAI_API_KEY environment variable not set\n")
		os.Exit(1)
	}

	processor := &LinkProcessor{
		client: openai.NewClient(apiKey),
	}

	err := processor.ProcessFile(inputFile, outputFile, inPlace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type LinkProcessor struct {
	client *openai.Client
}

func (lp *LinkProcessor) ProcessFile(inputFile, outputFile string, inPlace bool) error {
	content, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	text := string(content)
	links := lp.extractLinks(text)

	if len(links) == 0 {
		fmt.Println("No links found to process.")
		return nil
	}

	fmt.Printf("Found %d links to process:\n\n", len(links))

	// Process each link
	for i := range links {
		if lp.isURL(links[i].Description) {
			fmt.Printf("Link %d: [%s](%s) - Already a URL, skipping\n", i+1, links[i].Text, links[i].Description)
			links[i].URL = links[i].Description
			links[i].Settled = true
			continue
		}

		err := lp.processLink(&links[i])
		if err != nil {
			fmt.Printf("Error processing link %d: %v\n", i+1, err)
			continue
		}
	}

	// Show final summary
	fmt.Println("\n=== Final Summary ===")
	for i, link := range links {
		status := "✓"
		if !link.Settled {
			status = "✗"
		}
		fmt.Printf("%s Link %d: [%s] → %s\n", status, i+1, link.Text, link.URL)
	}

	// Apply changes
	newContent := lp.applyChanges(text, links)

	if inPlace {
		err = os.WriteFile(inputFile, []byte(newContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write to input file: %w", err)
		}
		fmt.Printf("\nFile updated in-place: %s\n", inputFile)
	} else {
		err = os.WriteFile(outputFile, []byte(newContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Printf("\nOutput written to: %s\n", outputFile)
	}

	return nil
}

func (lp *LinkProcessor) extractLinks(text string) []LinkInfo {
	re := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	matches := re.FindAllStringSubmatch(text, -1)
	indices := re.FindAllStringIndex(text, -1)

	var links []LinkInfo
	for i, match := range matches {
		if len(match) >= 3 {
			// Find the sentence containing this link
			sentence := lp.extractSentence(text, indices[i][0])
			
			links = append(links, LinkInfo{
				Text:        match[1],
				Description: match[2],
				Sentence:    sentence,
				StartPos:    indices[i][0],
				EndPos:      indices[i][1],
			})
		}
	}

	return links
}

func (lp *LinkProcessor) extractSentence(text string, linkPos int) string {
	// Find sentence boundaries around the link
	start := linkPos
	end := linkPos

	// Go backward to find sentence start
	for start > 0 && text[start-1] != '.' && text[start-1] != '!' && text[start-1] != '?' && text[start-1] != '\n' {
		start--
	}

	// Go forward to find sentence end
	for end < len(text) && text[end] != '.' && text[end] != '!' && text[end] != '?' && text[end] != '\n' {
		end++
	}

	if end < len(text) {
		end++ // Include the punctuation
	}

	return strings.TrimSpace(text[start:end])
}

func (lp *LinkProcessor) isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "ftp://")
}

func (lp *LinkProcessor) processLink(link *LinkInfo) error {
	for !link.Settled {
		fmt.Printf("\n--- Processing Link ---\n")
		fmt.Printf("Text: %s\n", link.Text)
		fmt.Printf("Description: %s\n", link.Description)
		fmt.Printf("Context: %s\n", link.Sentence)

		url, confidence, reasoning, err := lp.findURL(link.Description, link.Sentence)
		if err != nil {
			fmt.Printf("API Error: %v\n", err)
			fmt.Print("Would you like to retry? (y/n): ")
			
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(response)
			
			if strings.ToLower(response) != "y" {
				fmt.Printf("Skipping this link.\n")
				return nil
			}
			continue
		}

		fmt.Printf("\nAI Reasoning: %s\n", reasoning)
		fmt.Printf("Suggested URL: %s\n", url)
		fmt.Printf("Confidence: %.1f%%\n", confidence*100)

		if confidence >= 0.8 {
			fmt.Print("Accept this URL? (y/n/m for more context): ")
		} else {
			fmt.Print("Low confidence. Add more context or provide URL? (m/u): ")
		}

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		switch response {
		case "y":
			if confidence >= 0.8 {
				link.URL = url
				link.Confidence = confidence
				link.Settled = true
				fmt.Printf("✓ Link settled: %s\n", url)
			} else {
				fmt.Println("Cannot accept - confidence too low. Please add more context.")
			}
		case "n":
			if confidence >= 0.8 {
				fmt.Print("Please add more context to the description: ")
				additionalContext, _ := reader.ReadString('\n')
				additionalContext = strings.TrimSpace(additionalContext)
				link.Description += " " + additionalContext
			}
		case "m":
			fmt.Print("Please add more context to the description: ")
			additionalContext, _ := reader.ReadString('\n')
			additionalContext = strings.TrimSpace(additionalContext)
			link.Description += " " + additionalContext
		case "u":
			fmt.Print("Please provide the exact URL: ")
			userURL, _ := reader.ReadString('\n')
			userURL = strings.TrimSpace(userURL)
			if lp.isURL(userURL) {
				link.URL = userURL
				link.Confidence = 1.0
				link.Settled = true
				fmt.Printf("✓ Link settled with user-provided URL: %s\n", userURL)
			} else {
				fmt.Println("Invalid URL format. Please try again.")
			}
		default:
			fmt.Println("Invalid response. Please try again.")
		}
	}

	return nil
}

func (lp *LinkProcessor) findURL(description, sentence string) (string, float64, string, error) {
	prompt := fmt.Sprintf(`Given this context: "%s"

Find the most appropriate URL for the description: "%s"

Provide your response in exactly this format:
URL: [the URL you found]
CONFIDENCE: [a number between 0.0 and 1.0]
REASONING: [brief explanation of why you chose this URL and how confident you are]

Be conservative with confidence scores. Only use 0.8+ if you're very sure this is the exact resource the user wants.`, sentence, description)

	resp, err := lp.client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: "gpt-4o-mini",
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
		},
	)

	if err != nil {
		return "", 0, "", err
	}

	if len(resp.Choices) == 0 {
		return "", 0, "", fmt.Errorf("no response from AI")
	}

	return lp.parseAIResponse(resp.Choices[0].Message.Content)
}

func (lp *LinkProcessor) parseAIResponse(response string) (string, float64, string, error) {
	lines := strings.Split(response, "\n")
	var url, reasoning string
	var confidence float64

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "URL:") {
			url = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		} else if strings.HasPrefix(line, "CONFIDENCE:") {
			confidenceStr := strings.TrimSpace(strings.TrimPrefix(line, "CONFIDENCE:"))
			fmt.Sscanf(confidenceStr, "%f", &confidence)
		} else if strings.HasPrefix(line, "REASONING:") {
			reasoning = strings.TrimSpace(strings.TrimPrefix(line, "REASONING:"))
		}
	}

	if url == "" {
		return "", 0, "", fmt.Errorf("could not parse URL from AI response")
	}

	return url, confidence, reasoning, nil
}

func (lp *LinkProcessor) applyChanges(text string, links []LinkInfo) string {
	// Sort links by position (descending) to avoid position shifts
	sortedLinks := make([]LinkInfo, len(links))
	copy(sortedLinks, links)

	for i := 0; i < len(sortedLinks); i++ {
		for j := i + 1; j < len(sortedLinks); j++ {
			if sortedLinks[i].StartPos < sortedLinks[j].StartPos {
				sortedLinks[i], sortedLinks[j] = sortedLinks[j], sortedLinks[i]
			}
		}
	}

	result := text
	for _, link := range sortedLinks {
		if link.Settled && link.URL != "" {
			oldLink := fmt.Sprintf("[%s](%s)", link.Text, link.Description)
			newLink := fmt.Sprintf("[%s](%s)", link.Text, link.URL)
			
			// Replace only the specific instance at the known position
			before := result[:link.StartPos]
			after := result[link.EndPos:]
			result = before + newLink + after
			
			// Update positions for remaining links
			sizeDiff := len(newLink) - len(oldLink)
			for i := range sortedLinks {
				if sortedLinks[i].StartPos < link.StartPos {
					sortedLinks[i].StartPos += sizeDiff
					sortedLinks[i].EndPos += sizeDiff
				}
			}
		}
	}

	return result
}