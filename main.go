package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

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
	Messages    []openai.ChatCompletionMessage
	RejectedURLs []string
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
	// Print the processing header once per link
	fmt.Printf("\n\033[1;36m╔══════════════════════════════════════════════════════════════════════════════════════╗\033[0m\n")
	fmt.Printf("\033[1;36m║                                    \033[37mPROCESSING LINK\033[1;36m                                    ║\033[0m\n")
	fmt.Printf("\033[1;36m╚══════════════════════════════════════════════════════════════════════════════════════╝\033[0m\n\n")

	// Show context with highlighted link
	contextWithHighlight := lp.highlightLinkInContext(link.Sentence, link.Text, link.Description)
	fmt.Printf("\033[33mContext:\033[0m %s\n", contextWithHighlight)

	for !link.Settled {
		fmt.Printf("🤖 Requesting new suggestion from AI...\n")
		url, confidence, reasoning, err := lp.findURLWithConversation(link)
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

		// Check if this URL was already rejected
		if lp.isURLRejected(url, link.RejectedURLs) {
			fmt.Printf("⚠️  AI suggested a previously rejected URL: %s\n", url)
			fmt.Println("Automatically asking AI for a different link...")
			
			// Automatically add feedback to conversation
			link.Messages = append(link.Messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: fmt.Sprintf("You already suggested %s and I rejected it. Please provide a different URL.", url),
			})
			continue
		}

		// Verify that the URL is accessible
		fmt.Printf("🔍 Verifying URL accessibility: %s...", url)
		isAccessible, statusCode := lp.verifyURL(url)
		if !isAccessible {
			fmt.Printf(" ❌ Failed (HTTP %d)\n", statusCode)
			fmt.Printf("⚠️  The suggested URL is not accessible. Asking AI for a working alternative...\n")
			
			// Add this broken URL to rejected list
			link.RejectedURLs = append(link.RejectedURLs, url)
			
			// Update the initial prompt to include this broken URL in the rejected list
			lp.updateInitialPromptWithRejectedURL(link, url)
			
			// Automatically add feedback to conversation
			link.Messages = append(link.Messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: fmt.Sprintf("The URL %s is not accessible (HTTP %d). Please provide a working URL that is NOT any of these rejected URLs: %s", url, statusCode, strings.Join(link.RejectedURLs, ", ")),
			})
			continue
		}
		fmt.Printf(" ✅ Accessible\n")

		// AI Reasoning in dark grey
		fmt.Printf("\n\033[90m%s\033[0m\n", reasoning)
		
		// Suggested URL in yellow with cyan URL
		fmt.Printf("\033[33mSuggested URL (%.0f%%):\033[0m \033[36m%s\033[0m\n", confidence*100, url)

		// User interaction with this specific URL
		for {
			fmt.Print("Accept this URL? (y to accept, v to view in browser, paste a URL to use it, or add context): ")

			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(response)

			switch strings.ToLower(response) {
			case "y":
				if confidence >= 0.8 {
					link.URL = url
					link.Confidence = confidence
					link.Settled = true
					fmt.Printf("✓ Link settled: %s\n", url)
					return nil
				} else {
					fmt.Println("Cannot accept - confidence too low. Please add more context.")
					// Need to get new AI suggestion - break out of both loops
					break
				}
			case "v":
				err := lp.openInBrowser(url)
				if err != nil {
					fmt.Printf("Error opening browser: %v\n", err)
				} else {
					fmt.Printf("Opened %s in browser\n", url)
				}
				// Continue in this inner loop - don't change any state
				continue
			default:
				// Check if the user provided a URL directly
				if lp.isURL(response) {
					fmt.Printf("Using your provided URL: %s\n", response)
					link.URL = response
					link.Confidence = 1.0
					link.Settled = true
					fmt.Printf("✓ Link settled with user-provided URL: %s\n", response)
					return nil
				}
				
				// Add this URL to rejected list since user is providing more context
				link.RejectedURLs = append(link.RejectedURLs, url)
				
				// Update the initial prompt to include this rejected URL
				lp.updateInitialPromptWithRejectedURL(link, url)
				
				// Treat any other input as additional context
				if response != "" {
					fmt.Printf("Added context: %s\n", response)
					// Add user's feedback to the conversation
					link.Messages = append(link.Messages, openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleUser,
						Content: fmt.Sprintf("%s (Note: Do NOT suggest any of these rejected URLs: %s)", response, strings.Join(link.RejectedURLs, ", ")),
					})
				} else {
					fmt.Print("Please add more context to the description: ")
					additionalContext, _ := reader.ReadString('\n')
					additionalContext = strings.TrimSpace(additionalContext)
					if additionalContext != "" {
						fmt.Printf("Added context: %s\n", additionalContext)
						// Add user's feedback to the conversation
						link.Messages = append(link.Messages, openai.ChatCompletionMessage{
							Role:    openai.ChatMessageRoleUser,
							Content: fmt.Sprintf("%s (Note: Do NOT suggest any of these rejected URLs: %s)", additionalContext, strings.Join(link.RejectedURLs, ", ")),
						})
					}
				}
				fmt.Println("Refining search with additional context...")
				// Break out of inner loop to get new AI suggestion
				break
			}
		}
	}

	return nil
}

func (lp *LinkProcessor) findURLWithConversation(link *LinkInfo) (string, float64, string, error) {
	// Initialize conversation if this is the first request
	if len(link.Messages) == 0 {
		rejectedURLsText := ""
		if len(link.RejectedURLs) > 0 {
			rejectedURLsText = fmt.Sprintf("\n\nIMPORTANT: Do NOT suggest any of these previously rejected URLs: %s", strings.Join(link.RejectedURLs, ", "))
		}

		initialPrompt := fmt.Sprintf(`Given this context: "%s"

Find the most appropriate URL for the description: "%s"

Provide your response in exactly this format:
URL: [the URL you found]
CONFIDENCE: [a number between 0.0 and 1.0]
REASONING: [brief explanation of why you chose this URL and how confident you are]

Be conservative with confidence scores. Only use 0.8+ if you're very sure this is the exact resource the user wants.

IMPORTANT: If the user provides feedback about a URL being wrong or broken, you MUST provide a completely different URL. Do not repeat URLs that have been rejected.%s`, link.Sentence, link.Description, rejectedURLsText)

		link.Messages = append(link.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: initialPrompt,
		})
	}

	resp, err := lp.client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:    "gpt-4o-mini",
			Messages: link.Messages,
		},
	)

	if err != nil {
		return "", 0, "", err
	}

	if len(resp.Choices) == 0 {
		return "", 0, "", fmt.Errorf("no response from AI")
	}

	// Add AI's response to the conversation
	link.Messages = append(link.Messages, resp.Choices[0].Message)

	return lp.parseAIResponse(resp.Choices[0].Message.Content)
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

func (lp *LinkProcessor) highlightLinkInContext(sentence, linkText, description string) string {
	// Find the markdown link in the sentence
	linkPattern := fmt.Sprintf(`\[%s\]\(%s\)`, regexp.QuoteMeta(linkText), regexp.QuoteMeta(description))
	re := regexp.MustCompile(linkPattern)
	
	// Replace with highlighted version
	highlighted := re.ReplaceAllStringFunc(sentence, func(match string) string {
		// Green for the entire link, bright green for the description
		return fmt.Sprintf("\033[32m[\033[92m%s\033[32m](\033[92m%s\033[32m)\033[0m", linkText, description)
	})
	
	// Ensure the rest of the text is white
	return fmt.Sprintf("\033[37m%s\033[0m", highlighted)
}

func (lp *LinkProcessor) openInBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}

func (lp *LinkProcessor) isURLRejected(url string, rejectedURLs []string) bool {
	for _, rejectedURL := range rejectedURLs {
		if url == rejectedURL {
			return true
		}
	}
	return false
}

func (lp *LinkProcessor) verifyURL(url string) (bool, int) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Make HEAD request to check if URL is accessible
	resp, err := client.Head(url)
	if err != nil {
		// If HEAD fails, try GET request (some servers don't support HEAD)
		resp, err = client.Get(url)
		if err != nil {
			return false, 0
		}
	}
	defer resp.Body.Close()

	// Consider 2xx and 3xx status codes as accessible
	statusCode := resp.StatusCode
	isAccessible := statusCode >= 200 && statusCode < 400
	
	return isAccessible, statusCode
}

func (lp *LinkProcessor) updateInitialPromptWithRejectedURL(link *LinkInfo, rejectedURL string) {
	// Update the first message in the conversation to include the new rejected URL
	if len(link.Messages) > 0 && link.Messages[0].Role == openai.ChatMessageRoleUser {
		// Extract the parts of the original prompt
		originalContent := link.Messages[0].Content
		
		// Find where the rejected URLs section starts or where to add it
		rejectedURLsText := fmt.Sprintf("\n\nIMPORTANT: Do NOT suggest any of these previously rejected URLs: %s", strings.Join(link.RejectedURLs, ", "))
		
		// Replace or add the rejected URLs section
		if strings.Contains(originalContent, "IMPORTANT: Do NOT suggest any of these previously rejected URLs:") {
			// Replace existing rejected URLs section
			re := regexp.MustCompile(`\n\nIMPORTANT: Do NOT suggest any of these previously rejected URLs: [^\n]*`)
			link.Messages[0].Content = re.ReplaceAllString(originalContent, rejectedURLsText)
		} else {
			// Add rejected URLs section at the end
			link.Messages[0].Content = originalContent + rejectedURLsText
		}
	}
}