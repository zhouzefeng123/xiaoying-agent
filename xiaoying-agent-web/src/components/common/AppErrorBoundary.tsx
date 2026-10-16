import React from 'react';

type AppErrorBoundaryState = {
  error: Error | null;
};

export class AppErrorBoundary extends React.Component<React.PropsWithChildren, AppErrorBoundaryState> {
  constructor(props: React.PropsWithChildren) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: Error): AppErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error('[Renderer] Unhandled render error:', error, errorInfo);
  }

  handleRetry = () => {
    this.setState({ error: null });
    window.location.reload();
  };

  render() {
    if (!this.state.error) {
      return this.props.children;
    }

    return (
      <div className="min-h-screen bg-zinc-50 text-zinc-900 flex items-center justify-center px-6">
        <div className="w-full max-w-xl rounded-2xl border border-zinc-200 bg-white shadow-sm p-6">
          <div className="text-lg font-semibold mb-2">界面渲染失败</div>
          <div className="text-sm text-zinc-600 leading-6 mb-4">
            前端热更新后触发了运行时异常，所以当前窗口没有自动恢复。
          </div>
          <pre className="text-xs leading-5 bg-zinc-100 rounded-lg p-4 overflow-auto whitespace-pre-wrap break-words mb-4">
            {this.state.error.stack || this.state.error.message}
          </pre>
          <button
            onClick={this.handleRetry}
            className="px-4 py-2 rounded-lg bg-zinc-900 text-white text-sm hover:bg-zinc-700 transition-colors"
          >
            重新加载界面
          </button>
        </div>
      </div>
    );
  }
}