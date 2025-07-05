# SerpAPI Integration for link-summoner

## Purpose & User Problem

When writing Markdown, users often insert placeholder text in link URLs (e.g., `[Revolutionary War](revolutionary war wiki)`) to avoid breaking their writing flow. Manually searching for and inserting the correct URLs later is tedious and distracting. The goal is to automate this process by using AI to generate a search query and then fetch the top results from SerpAPI, allowing the user to quickly select the best link.

## Success Criteria

- The tool identifies all Markdown links with non-URL descriptions in a document.
- For each such link, the tool:
  - Uses OpenAI to generate a high-quality search query based on the link text, description, and surrounding context.
  - Uses SerpAPI to fetch the top 5 Google search results for that query.
  - Presents these results to the user in the terminal, with the first result highlighted.
  - Allows the user to:
    - Press `y` to accept the highlighted link.
    - Press `v` to view the highlighted link in their browser.
    - Press `yX` (e.g., `y3`) to accept link #3.
    - Press `vX` (e.g., `v3`) to view link #3.
    - Enter a custom URL or add more context to refine the search.
- The selected URL replaces the placeholder in the Markdown file.
- The process is repeatable for all links in the document.

## Scope & Constraints

- Only links with non-URL descriptions are processed; existing URLs are skipped.
- The tool must work in a terminal/CLI environment.
- Only the top 5 results from SerpAPI are shown.
- The first result is always the default "highlighted" link.
- The user can skip a link or provide additional context to refine the search.
- The tool must handle network/API errors gracefully and allow retrying or skipping.

## Technical Considerations

- OpenAI is used only to generate the search query, not to fetch or rank URLs.
- SerpAPI is used to fetch Google search results.
- The tool should be efficient and not re-query OpenAI/SerpAPI unnecessarily.
- API keys for OpenAI and SerpAPI are provided via environment variables.
- The CLI should be responsive and user-friendly, with clear prompts and colorized output.

## Out of Scope

- Browser-based or GUI interfaces.
- Support for search engines other than Google (for now).
- Bulk/batch processing without user interaction.
- Automated acceptance of links without user review.
- Modularity for swapping out search engines in the future. 