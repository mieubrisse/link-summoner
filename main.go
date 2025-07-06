package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/sashabaranov/go-openai"
	serpapi "github.com/serpapi/serpapi-golang"
)

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type LinkInfo struct {
	Text          string
	Description   string
	URL           string
	Confidence    float64
	Sentence      string
	StartPos      int
	EndPos        int
	Settled       bool
	Messages      []openai.ChatCompletionMessage
	SearchQuery   string
	SearchResults []SearchResult
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

	openaiApiKey := os.Getenv("OPENAI_API_KEY")
	if openaiApiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: OPENAI_API_KEY environment variable not set\n")
		os.Exit(1)
	}

	serpapiApiKey := os.Getenv("SERPAPI_API_KEY")
	if serpapiApiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: SERPAPI_API_KEY environment variable not set\n")
		os.Exit(1)
	}

	serpapiSetting := serpapi.NewSerpApiClientSetting(serpapiApiKey)
	serpapiSetting.Engine = "google"
	serpapiClient := serpapi.NewClient(serpapiSetting)

	processor := &LinkProcessor{
		openaiClient:  openai.NewClient(openaiApiKey),
		serpapiClient: serpapiClient,
	}

	err := processor.ProcessFile(inputFile, outputFile, inPlace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type LinkProcessor struct {
	openaiClient  *openai.Client
	serpapiClient serpapi.SerpApiClient
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
		// Generate search query using OpenAI
		searchQuery, err := lp.generateSearchQuery(link)
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

		// Fetch search results using SerpAPI
		searchResults, err := lp.fetchSearchResults(searchQuery)
		if err != nil {
			fmt.Printf("SerpAPI Error: %v\n", err)
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

		// Store search results in link info
		link.SearchQuery = searchQuery
		link.SearchResults = searchResults

		// Present results to user
		lp.presentSearchResults(link)

		// Handle user interaction
		for {
			fmt.Print("Choose an option (yX/vX for specific result, or add context): ")

			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(response)

			if response == "" {
				continue
			}

			// Handle yX/vX commands (e.g., y3, v2)
			if len(response) >= 2 && (response[0] == 'y' || response[0] == 'v') {
				indexStr := response[1:]
				index, err := strconv.Atoi(indexStr)
				if err == nil && index >= 1 && index <= len(link.SearchResults) {
					resultIndex := index - 1 // Convert to 0-based index

					if response[0] == 'y' {
						link.URL = link.SearchResults[resultIndex].URL
						link.Confidence = 1.0
						link.Settled = true
						fmt.Printf("✓ Link settled: %s\n", link.URL)
						return nil
					} else { // response[0] == 'v'
						err := lp.openInBrowser(link.SearchResults[resultIndex].URL)
						if err != nil {
							fmt.Printf("Error opening browser: %v\n", err)
						} else {
							fmt.Printf("Opened %s in browser\n", link.SearchResults[resultIndex].URL)
						}
						continue
					}
				} else {
					fmt.Printf("Invalid result number. Please choose 1-%d.\n", len(link.SearchResults))
					continue
				}
			}

			// Check if the user provided a URL directly
			if lp.isURL(response) {
				fmt.Printf("Using your provided URL: %s\n", response)
				link.URL = response
				link.Confidence = 1.0
				link.Settled = true
				fmt.Printf("✓ Link settled with user-provided URL: %s\n", response)
				return nil
			}

			// Treat any other input as additional context
			if response != "" {
				fmt.Printf("Added context: %s\n", response)
				// Add user's feedback to the conversation for next search query generation
				link.Messages = append(link.Messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: fmt.Sprintf("The previous search results weren't quite right. Please refine the search query with this additional context: %s", response),
				})
				fmt.Println("Refining search with additional context...")
				break // Break out of inner loop to get new search query
			}
		}
	}

	return nil
}

func (lp *LinkProcessor) generateSearchQuery(link *LinkInfo) (string, error) {
	// Initialize conversation if this is the first request
	if len(link.Messages) == 0 {
		initialPrompt := fmt.Sprintf(`Given this context: "%s"

Generate a search query to find the most appropriate URL for the description: "%s"

Provide your response in exactly this format:
QUERY: [the search query you generated]

The search query should be:
- Specific enough to find the exact resource the user wants
- General enough to return relevant results
- Include key terms from both the link text and description
- Be optimized for web search
- NOT include any quotes around the query

Example: If the link text is "Python requests" and description is "HTTP library documentation", a good query might be "Python requests library official documentation"`, link.Sentence, link.Description)

		link.Messages = append(link.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: initialPrompt,
		})
	}

	resp, err := lp.openaiClient.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:    "gpt-4o-mini",
			Messages: link.Messages,
		},
	)

	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from AI")
	}

	// Add AI's response to the conversation
	link.Messages = append(link.Messages, resp.Choices[0].Message)

	// Parse the search query from the response
	query := lp.parseSearchQuery(resp.Choices[0].Message.Content)
	if query == "" {
		return "", fmt.Errorf("could not parse search query from AI response")
	}

	return query, nil
}

func (lp *LinkProcessor) parseSearchQuery(response string) string {
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "QUERY:") {
			query := strings.TrimSpace(strings.TrimPrefix(line, "QUERY:"))
			// Remove any surrounding quotes
			query = strings.Trim(query, `"'`)
			return query
		}
	}
	return ""
}

func (lp *LinkProcessor) fetchSearchResults(query string) ([]SearchResult, error) {
	// Mock results for testing - replace with actual SerpAPI call when ready

	// Generate mock results based on the query
	mockResults := []SearchResult{
		{
			Title:   fmt.Sprintf("Official %s Documentation", query),
			URL:     fmt.Sprintf("https://example.com/%s-docs", strings.ToLower(strings.ReplaceAll(query, " ", "-"))),
			Snippet: fmt.Sprintf("Comprehensive documentation for %s. Learn how to use %s effectively with examples and tutorials.", query, query),
		},
		{
			Title:   fmt.Sprintf("%s - Wikipedia", query),
			URL:     fmt.Sprintf("https://en.wikipedia.org/wiki/%s", strings.ReplaceAll(query, " ", "_")),
			Snippet: fmt.Sprintf("Wikipedia article about %s. History, background, and detailed information about %s.", query, query),
		},
		{
			Title:   fmt.Sprintf("%s Tutorial for Beginners", query),
			URL:     fmt.Sprintf("https://tutorials.example.com/%s-guide", strings.ToLower(strings.ReplaceAll(query, " ", "-"))),
			Snippet: fmt.Sprintf("Step-by-step tutorial for learning %s. Perfect for beginners who want to get started with %s.", query, query),
		},
		{
			Title:   fmt.Sprintf("%s Best Practices", query),
			URL:     fmt.Sprintf("https://blog.example.com/%s-best-practices", strings.ToLower(strings.ReplaceAll(query, " ", "-"))),
			Snippet: fmt.Sprintf("Learn the best practices for working with %s. Tips, tricks, and common pitfalls to avoid when using %s.", query, query),
		},
		{
			Title:   fmt.Sprintf("%s Examples and Code Samples", query),
			URL:     fmt.Sprintf("https://github.com/example/%s-examples", strings.ToLower(strings.ReplaceAll(query, " ", "-"))),
			Snippet: fmt.Sprintf("Collection of practical examples and code samples for %s. Real-world use cases and implementation patterns.", query, query),
		},
	}

	return mockResults, nil
}

func (lp *LinkProcessor) presentSearchResults(link *LinkInfo) {
	if len(link.SearchResults) == 0 {
		fmt.Println("No search results found.")
		return
	}

	fmt.Printf("Query: \"%s\"\n\n", link.SearchQuery)

	for i, result := range link.SearchResults {
		// Title (cyan) - no extra spacing
		fmt.Printf("\033[36m%d. %s\033[0m\n", i+1, result.Title)

		// Description (white) - 2 spaces indent, truncate if too long
		snippet := result.Snippet
		if len(snippet) > 120 {
			snippet = snippet[:117] + "..."
		}
		fmt.Printf("   %s\n", snippet)

		// URL (dark blue) - 2 spaces indent
		fmt.Printf("   \033[34m%s\033[0m\n", result.URL)
		fmt.Println()
	}

	fmt.Printf("\033[1;33mCommands:\033[0m yX=accept result X, vX=view result X\n")
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
