**이 파일은 작업용 프롬프트를 기록한 파일로 작업시 참고 하지 않는다.**

# 첫번쨰 기능 구현 프롬프트
- LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.
- REQUIREMENTS.md 에 정의된 요구사항들에 따라 AI 대화기능을 제공하는 CLI 프로그램 제작.

# config 및 session 디렉토리 위치 조정
- config.json 파일과 system_prompt.txt, 세션 파일 위치를 다음을 참고해 변경 해줘
    - config.json 파일: $HOME/.humble-ai-cli/config.json
    - system_prompt.txt 파일: $HOME/.humble-ai-cli/system_prompt.txt
    - 대화 세션 디렉토리: $HOME/.humble-ai-cli/sessions/

# 새 세션 생성 커맨트 추가
- 새 세션을 생성하고 전환하는 커맨드 추가 해줘.
- /new 커맨드 입력시 새로운 세션을 메모리에 생성
- 새 세션에서 새로운 대화를 입력하고 답변을 받으면 세션 파일을 생성 할 것.
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# MCP Server 호출 기능 추가
- MCP Server 설정은 $HOME/.humble-ai-cli/mcp_servers 디렉토리에 각 mcp server 에 대한 json 설정 파일로 관리됨
  - MCP Server 설정에는 enable/disable 을 설정 할 수 있고 enable 된 MCP Server 만 initialize 하고 호출 할 수 있음
- LLM 이 필요시 MCP Server 호출을 요청 할 수 있고 humble-ai-cli 를 MCP Server 를 호출하고 결과를 LLM 에게 전달 함
- 정확한 답변을 위해 LLM 은 MCP Server 를 여러번 호출 할 수 있음
- MCP Server 호출 전에는 사용자 에게 어떤 mcp 를 호출 하는지 설명하고 Y/N 입력을 요청하고 Y 입력시 호출하고 N 입력시 작업을 중단 함.
- MCP 관련 기능은 github.com/modelcontextprotocol/go-sdk 의 mcp 패키지를 이용해 MCP Client 기능을 구현하고 패키지 사용 가이드는 다음 URL 을 참고 할 것
  - https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp
- Default system_prompt.txt 생성 기능 추가
  - 최초 실행 시 system_prompt.txt 파일의 존재 여부를 확인하고 미 존재시 Default system_prompt.txt 를 생성 할 것.
  - system_prompt.txt 에는 MCP server 호출을 위한 tooling 정의가 포함되어야 함.
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Ollama API tooling 지원
- Ollama 에서도 MCP 를 사용 할 수 있도록 API 호출 시 tool 설정 추가
- Ollama chat api request sample
```shell
curl http://localhost:11434/api/chat -d '{
  "model": "llama3.2",
  "messages": [
    {
      "role": "user",
      "content": "what is the weather in tokyo?"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the weather in a given city",
        "parameters": {
          "type": "object",
          "properties": {
            "city": {
              "type": "string",
              "description": "The city to get the weather for"
            }
          },
          "required": ["city"]
        }
      }
    }
  ],
  "stream": false 
}'
```
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Config 파일 Schema 수정
- Config.json 에서 root 의 provider 와 activeModel 은 삭제
- models 의 각 model 설정에 active 항목을 추가하고, true 면 해당 model 이 활성화 된 것으로 처리
- 만약 active true 인 모델이 없을 경우. 사용자가 prompt 입력시 /set-model 커맨드를 통해 모델을 먼저 선택 할 것을 가이드
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# Prompt 입력 시 커서 이동 기능 추가
- 좌우 방향키와, Home, End 키를 통해 커서를 이동 시킬 수 있어야 함

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**
