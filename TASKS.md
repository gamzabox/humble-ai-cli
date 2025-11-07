# 첫번째 기능 구현 프롬프트
- [x] REQUIREMENTS.md와 LLM_RULES.md를 검토하고 작업 계획을 수립했다.
- [x] 요구사항을 검증하는 테스트 코드를 작성한다.
- [x] 테스트를 통과하도록 CLI 기능을 구현한다.
- [x] 모든 테스트를 실행해 통과 여부를 확인하고 필요한 문서를 갱신한다.

# 새 세션 생성 커맨트 추가
- [x] /new 커맨드 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] /new 커맨드 동작을 검증하는 테스트를 추가한다.
- [x] /new 커맨드 구현을 완료한다.
- [x] 전체 테스트를 실행하고 문서 업데이트를 검증한다.

# MCP Server 호출 기능 추가
- [x] 요구사항을 검증하는 테스트를 작성한다.
- [x] MCP 연동 및 system prompt 초기화 로직을 구현한다.
- [x] 전체 테스트를 실행하고 필요한 문서를 갱신한다.

# MCP 커맨드 추가
- [x] /mcp 커맨드 요구사항을 테스트로 정의한다.
- [x] MCP function 리스트 출력 기능을 구현한다.
- [x] 전체 테스트를 실행하고 문서를 갱신한다.

# Logging 기능 추가
- [x] 요구사항을 검증하는 테스트를 작성한다.
- [x] 로그 구성 및 기록 기능을 구현한다.
- [x] 전체 테스트를 실행하고 문서를 갱신한다.

# Ollama MCP Tooling 지원
- [x] Ollama MCP tool 전달 요구사항을 검증하는 테스트를 추가한다.
- [x] Ollama provider 에 MCP tool 호출 처리 로직을 구현한다.
- [x] 전체 테스트를 실행하고 관련 문서를 업데이트한다.

# Config Schema 수정
- [x] 새로운 config schema 요구사항을 테스트로 정의한다.
- [x] 모델 활성화 플래그 기반 config 로직과 /set-model 안내를 구현한다.
- [x] 전체 테스트를 실행하고 문서를 갱신한다.

# MCP Session 재사용
- [x] MCP 세션 재사용 및 종료 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] MCP 세션 재사용 및 종료 동작을 검증하는 테스트를 작성한다.
- [x] MCP Manager 에 세션 캐싱과 종료 로직을 구현하고 App 종료 시 모든 세션을 닫는다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# Thinking 메시지 스트리밍 표시
- [x] Thinking 메시지 스트리밍 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] Thinking 메시지 스트리밍 동작을 검증하는 테스트를 먼저 작성한다.
- [x] Thinking 메시지 스트리밍 구현을 완료한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)
- [x] OpenAI/Ollama reasoning payload 를 파싱해 실제 thinking 내용을 스트리밍 한다.
- [x] reasoning 스트리밍 동작을 테스트로 검증한다.

# Tool call 로그 보강
- [x] Tool call 수행 시 LLM request 로그 누락 문제를 파악한다.
- [x] Tool call 이후 LLM Request/Response 로그 기록을 검증하는 테스트를 추가한다.
- [x] Tool call 이후 LLM Request/Response 로그가 남도록 구현을 보강한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# 프롬프트 커서 이동 기능
- [x] 프롬프트 입력 커서 이동 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] 커서 이동 및 다국어 입력 동작을 검증하는 테스트를 추가한다.
- [x] 커서 이동 기능을 구현한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# Windows Terminal 커서 이동 문제 수정
- [x] Windows 터미널 커서 이동 요구사항을 검토하고 필요시 REQUIREMENTS.md를 업데이트한다.
- [x] Windows 입력 시퀀스에 대한 커서 이동 테스트를 추가한다.
- [x] Windows 입력 시퀀스를 처리하도록 구현을 수정한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)
