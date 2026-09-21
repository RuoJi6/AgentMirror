# AgentMirror

AgentMirror 是一框专门针对渗透测试智能体反制的蜜罐，以AI对抗AI方式。

核心功能点（agent只负责生成测试修改蜜罐及提示词，但是没有自动交互功能）：
- 对话生成蜜罐：通过 Agent 创建和调整蜜罐，支持快速克隆真实业务页面外观。
- 多站点联动：一个工作区构建多个关联站点，支持嵌套路径和独立端口，模拟复杂业务环境。
- 自定义请求与响应：根据路径、参数、请求头及正文匹配流量，返回不同响应，模拟业务及漏洞特征。
- 提示词精准投放：按站点、接口和触发条件绑定提示词，支持动态变量及版本管理。
- 自动核查与红队测试：检查蜜罐访问、交付和回传链路，评估被测 Agent 对提示词注入的行为。
- 探索路径可视化：展示访问、探测、规则命中、文件下载及回传过程，支持证据查看与导出。
- 素材复用与文件托管：自由组合站点、场景和提示词，支持文件托管及下载追踪。
- 灵活部署与扩展：支持多端口管理、配置热更新、多模型接入及 MCP 工具扩展。

## 截图预览


| 仪表盘（总览 / 活动流） | 最近会话 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/d6a82916-073a-4188-a884-e0db3d8247a6" /> | <img width="1337" height="832" alt="image" src="https://github.com/user-attachments/assets/9f276329-486a-4f1c-9e58-50815909edd9" /> |

| 工作区 | 暴露端口 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/c1a7c310-5797-4d79-b8f4-ec872291d87c" /> | <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/0e0c85eb-249f-4e16-a232-d3f860945a24" /> |

| 提示词 | 提示词修改差异化 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/64d4341c-3159-47d4-b306-fc5bde1b761d" /> | <img width="1217" height="797" alt="image" src="https://github.com/user-attachments/assets/ce6312ad-bf28-4a5f-bb0b-4aa350f4424d" /> |

| agent操作 |
| :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/3fcd24e5-291e-4338-a062-67d3b11181ce" /> |

| 红队agent模拟攻击路径 | 文件托管 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/9ab2f81e-2708-4863-9160-bad6d38d7d9c" /> | <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/98c0c339-4769-45f7-9a86-c18792a091a9" />|

# 评估结果
使用chatgpt自动针对主流智能体进行评估，使用不同模型来评估蜜罐成果：
