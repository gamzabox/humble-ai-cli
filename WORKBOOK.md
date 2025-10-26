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