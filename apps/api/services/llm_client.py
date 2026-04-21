from typing import Type, Optional, Any
import logging
from langchain_openai import ChatOpenAI
from langchain_core.outputs import ChatGeneration
from pydantic import BaseModel

from apps.api.config import settings

logger = logging.getLogger(__name__)


class LLMClient:
    def __init__(self):
        self.model = settings.LLM_MODEL
        self.api_key = settings.LLM_API_KEY
        self.base_url = settings.LLM_BASE_URL
        self._client: Optional[ChatOpenAI] = None

    @property
    def client(self) -> ChatOpenAI:
        if self._client is None:
            self._client = ChatOpenAI(
                model=self.model,
                api_key=self.api_key,
                base_url=self.base_url,
                temperature=0.1,
                max_tokens=4000,
            )
        return self._client

    async def structured_output(
        self,
        prompt: str,
        output_schema: Type[BaseModel],
        trace_id: Optional[str] = None
    ) -> BaseModel:
        from langchain_core.output_parsers import JsonOutputParser
        from langchain_core.prompts import PromptTemplate

        parser = JsonOutputParser(pydantic_object=output_schema)

        prompt_template = PromptTemplate(
            template="{prompt}\n\n{format_instructions}",
            input_variables=["prompt"],
            partial_variables={"format_instructions": parser.get_format_instructions()}
        )

        chain = prompt_template | self.client | parser

        try:
            result = await chain.ainvoke({"prompt": prompt})
            if isinstance(result, dict):
                return output_schema(**result)
            return result
        except Exception as e:
            logger.error(f"LLM structured output failed: {e}")
            raise

    async def chat(
        self,
        system: str,
        user: str,
        response_format: str = "text",
        trace_id: Optional[str] = None
    ) -> str:
        from langchain_core.messages import HumanMessage, SystemMessage

        messages = [SystemMessage(content=system), HumanMessage(content=user)]

        try:
            response = await self.client.ainvoke(messages)
            if isinstance(response, ChatGeneration):
                return response.message.content
            return response.content if hasattr(response, 'content') else str(response)
        except Exception as e:
            logger.error(f"LLM chat failed: {e}")
            raise


llm_client = LLMClient()