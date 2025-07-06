# link-summoner
When writing in Markdown, stopping to fill in link URLs can distract you into your browser.

To keep you in the flow state, you can drop plaintext into the link URL (between parentheses) and then clean them up later with this tool.

For example, you might write:

```
The [Revolutionary War](revolutionary war wiki)

This tool keeps you in the flow state by letting you drop plaintext into the link URL, and then uses AI to generate search queries and SerpAPI to find the actual URLs.

## How it works

1. **AI Query Generation**: OpenAI generates an optimized search query based on your link text, description, and surrounding context
2. **Search Results**: SerpAPI fetches the top 5 Google search results for that query
3. **Interactive Selection**: You can:
   - Press `y` to accept the highlighted (first) result
   - Press `v` to view the highlighted result in your browser
   - Press `yX` to accept a specific result (e.g., `y3` accepts result #3)
   - Press `vX` to view a specific result (e.g., `v3` views result #3)
   - Enter a custom URL directly
   - Add more context to refine the search

## Setup

1. Set your API keys as environment variables:
   ```bash
   export OPENAI_API_KEY="your-openai-api-key"
   export SERPAPI_API_KEY="your-serpapi-api-key"
   ```

2. Build the tool:
   ```bash
   bash build.sh
   ```

## Usage

```bash
# Process a file and output to a new file
./build/link-summoner input.md output.md

# Process a file in-place
./build/link-summoner -i input.md
```

## Testing

**Note**: The tool currently runs in mock mode to preserve your SerpAPI quota. Mock results are generated based on your search queries. To enable real SerpAPI calls, uncomment the relevant section in `main.go`.

## Example

Input file `test.md`:
```markdown
# Sample Document

This is a test document with various links to test the link-summoner tool.

First, let's reference [Python's requests library](documentation for making HTTP requests in Python).

Here's an existing link that should be skipped: [Google](https://www.google.com).

Then, let's add a link to [React hooks](React hooks tutorial for beginners).
```

The tool will:
1. Skip the existing Google link (already a URL)
2. Generate search queries for "Python's requests library" and "React hooks"
3. Present top 5 results for each (mock results in test mode)
4. Let you select the best URLs interactively
