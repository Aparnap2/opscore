from typing import List, Dict, Optional
import re
import logging
from datetime import datetime

logger = logging.getLogger(__name__)

try:
    from crawl4ai import AsyncWebCrawler, CrawlerRunConfig
    from crawl4ai.content_filter_strategy import PruningContentFilter

    CRAWL4AI_AVAILABLE = True
except ImportError:
    CRAWL4AI_AVAILABLE = False
    logger.warning("Crawl4AI not available")


REGULATORY_SOURCES = {
    "sebi": "https://www.sebi.gov.in/legal/circulars.html",
    "rbi": "https://www.rbi.org.in/Scripts/BS_CircularIndexDisplay.aspx",
    "gst": "https://cbic-gst.gov.in/gst-goods-services-rates.html",
}


class ComplianceScraper:
    def __init__(self):
        self.sources = REGULATORY_SOURCES

    async def scrape_regulatory_updates(
        self, source: str, since_date: Optional[str] = None
    ) -> List[Dict]:
        if not CRAWL4AI_AVAILABLE:
            logger.warning("Crawl4AI not available, returning empty results")
            return []

        url = self.sources.get(source.lower())
        if not url:
            return []

        try:
            config = CrawlerRunConfig(
                content_filter=PruningContentFilter(threshold=0.5),
                word_count_threshold=200,
                exclude_external_links=True,
            )

            async with AsyncWebCrawler() as crawler:
                result = await crawler.arun(url=url, config=config)

            if not result or not result.success:
                logger.error(f"Failed to crawl {source}: {result.error if result else 'No result'}")
                return []

            pdf_links = self._extract_pdf_links(result.markdown)

            return [
                {
                    "source": source,
                    "title": self._extract_title(result.markdown),
                    "url": link,
                    "scraped_at": datetime.utcnow().isoformat(),
                }
                for link in pdf_links
            ]

        except Exception as e:
            logger.error(f"Error scraping {source}: {e}")
            return []

    def _extract_pdf_links(self, markdown: str) -> List[str]:
        pdf_pattern = r'https?://[^\s<>"]+\.pdf'
        return re.findall(pdf_pattern, markdown)

    def _extract_title(self, markdown: str) -> str:
        lines = markdown.split("\n")
        for line in lines[:10]:
            line = line.strip()
            if line and len(line) > 10:
                return line[:200]
        return "Untitled Document"

    async def scrape_all_sources(
        self, since_date: Optional[str] = None
    ) -> Dict[str, List[Dict]]:
        results = {}
        for source in self.sources.keys():
            results[source] = await self.scrape_regulatory_updates(source, since_date)
        return results


scraper = ComplianceScraper()