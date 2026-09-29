import React from "react";
import { RefreshCw, ArrowLeft, CircleAlert } from "lucide-react";
import { Button } from "../components/UI";
import "./flow-error.css";

// Suspense handles pending imports, but not rejected imports or render errors.
// Keep the editor mounted so an unavailable canvas cannot erase unsaved work.
export default class FlowErrorBoundary extends React.Component {
  state = { error: null, busy: false, recoveryError: "" };

  static getDerivedStateFromError(error) {
    return { error };
  }

  refresh = async () => {
    this.setState({ busy: true, recoveryError: "" });
    try {
      await this.props.beforeRefresh?.();
      window.location.reload();
    } catch (error) {
      this.setState({ busy: false, recoveryError: error.message });
    }
  };

  render() {
    if (!this.state.error) return this.props.children;
    const message = String(this.state.error?.message || this.state.error);
    const loadError =
      /dynamically imported|dynamic import|module script|importing a module|loading (?:css )?chunk|css.*preload|preload.*css/i.test(
        message,
      );
    return (
      <section className="flow-load-error" aria-label="画板恢复" role="alert">
        <h3>
          <CircleAlert size={20} />
          {loadError ? "画板资源加载失败" : "画板暂时无法显示"}
        </h3>
        <p>
          {loadError
            ? "服务升级或网络中断后，当前页面可能无法读取画板资源。刷新页面可重新加载。"
            : "画板渲染发生异常，其他工作区功能仍可使用。可以返回 AI 助手查看报告，或刷新后重试。"}
        </p>
        <p className="muted">
          {this.props.beforeRefresh
            ? "工作区有未保存的修改，将先保存，成功后再刷新。也可以返回继续编辑。"
            : "工作区与对话记录仍保留。刷新前请保存未保存的修改和未发送的文字。"}
        </p>
        <div className="flow-recovery-actions">
          <Button
            icon={RefreshCw}
            variant="primary"
            loading={this.state.busy}
            onClick={this.refresh}
          >
            {this.props.beforeRefresh ? "保存工作区并刷新" : "刷新页面"}
          </Button>
          {this.props.onBack && (
            <Button
              icon={ArrowLeft}
              disabled={this.state.busy}
              onClick={this.props.onBack}
            >
              返回 AI 助手
            </Button>
          )}
        </div>
        {this.state.recoveryError && (
          <p className="composer-error">
            保存未完成，页面未刷新：{this.state.recoveryError}
          </p>
        )}
        <details>
          <summary>错误信息</summary>
          <pre>{message}</pre>
        </details>
      </section>
    );
  }
}
