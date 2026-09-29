import React, { useState } from "react";
import { ArrowUpRight, ExternalLink } from "lucide-react";
import { useApp } from "../App";
import Ports from "./Ports";
import { AccountSecurity } from "./Auth";
import CollectResponseSettings from "./CollectResponseSettings";
import { GenerationSettings } from "./Composer";
import { Button, PageHeader, Panel } from "../components/UI";
export default function Settings() {
  const { data, navigate } = useApp();
  const [modelOpen, setModelOpen] = useState(false);
  return (
    <>
      <PageHeader
        title="系统设置"
        description="配置测试端口、回传响应与模型服务。"
      />
      <Ports />
      <CollectResponseSettings />
      <Panel title="站点与场景生成模型">
        <div className="padded">
          <p>
            配置多个模型服务，在对话中切换，用自然语言生成可编辑的前端和模拟响应。
          </p>
          <Button onClick={() => setModelOpen(true)}>配置生成模型</Button>
        </div>
      </Panel>
      {modelOpen && <GenerationSettings onClose={() => setModelOpen(false)} />}
      <div className="settings-layout">
        <div>
          <AccountSecurity />
          <Panel title="运行信息">
            <div className="detail-pairs padded">
              <span>服务版本</span>
              <strong>
                {data.runtime.backend === "go"
                  ? `Go · ${data.runtime.version}`
                  : "Python · v2"}
              </strong>
              <span>管理后台监听</span>
              <code>
                {data.runtime.admin_host}:{data.runtime.admin_port}
              </code>
              <span>管理后台配置</span>
              <strong>由启动参数指定，修改后重启生效</strong>
              <span>当前数据库</span>
              <code>{data.runtime.db_path}</code>
              <span>旧版数据</span>
              <strong>data/agentmirror.sqlite3 保留，未自动导入</strong>
            </div>
          </Panel>
        </div>
        <div>
          <Panel title="一次完整的测试">
            <ol className="guide-list">
              <li>
                <strong>准备方案</strong>
                <p>选择方案分组，编写正文，用结果占位符描述回传内容。</p>
                <button
                  className="text-button"
                  onClick={() => navigate("profiles")}
                >
                  前往提示词方案
                  <ArrowUpRight size={15} />
                </button>
              </li>
              <li>
                <strong>组合与发布工作区</strong>
                <p>
                  组合站点、模拟场景与指定提示词，预演后发布并绑定测试端口。
                </p>
                <button
                  className="text-button"
                  onClick={() => navigate("composer")}
                >
                  打开蜜罐工作区
                  <ArrowUpRight size={15} />
                </button>
              </li>
              <li>
                <strong>交给被测 Agent</strong>
                <p>
                  在你控制的测试环境中，让 Agent
                  访问此地址。页面不会自行采集系统信息。
                </p>
                <p>复制上方对应测试端口的访问地址。</p>
              </li>
              <li>
                <strong>检查证据</strong>
                <p>
                  在会话详情查看实际投放正文、事件时间线和回传。结合 Agent
                  日志标记实验结论。
                </p>
              </li>
            </ol>
          </Panel>
          <Panel title="界面参考">
            <div className="reference-links">
              <a
                href="https://github.com/Autumn-27/ARTEX"
                target="_blank"
                rel="noreferrer"
              >
                ARTEX · 管理台布局
                <ExternalLink size={15} />
              </a>
              <a
                href="https://github.com/arhamkhnz/next-shadcn-admin-dashboard"
                target="_blank"
                rel="noreferrer"
              >
                Studio Admin · 表单与表格
                <ExternalLink size={15} />
              </a>
            </div>
          </Panel>
        </div>
      </div>
    </>
  );
}
