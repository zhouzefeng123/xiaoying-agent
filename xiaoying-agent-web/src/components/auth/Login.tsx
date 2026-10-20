import React, { useEffect, useState } from 'react';
import { useAuth } from '../../contexts/useAuth';
import { toast } from 'sonner';
import { Loader2, Mail, Key, Check, X } from 'lucide-react';
import { cn } from '../../lib/utils';
import { Logo } from '../common/Logo';

export const Login: React.FC = () => {
    const { login, sendCode } = useAuth();
    const [email, setEmail] = useState('');
    const [code, setCode] = useState('');
    const [checked, setChecked] = useState(false);
    const [isLoading, setIsLoading] = useState(false);
    const [countdown, setCountdown] = useState(0);
    const [isSendingCode, setIsSendingCode] = useState(false);

    useEffect(() => {
        if (countdown <= 0) return;
        const timer = setInterval(() => setCountdown(c => c - 1), 1000);
        return () => clearInterval(timer);
    }, [countdown]);

    const handleSendCode = async () => {
        if (isSendingCode || countdown > 0) return;

        const normalizedEmail = email.trim();
        if (!normalizedEmail) {
            toast.error('请输入邮箱地址');
            return;
        }

        const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
        if (!emailRegex.test(normalizedEmail)) {
            toast.error('请输入有效的邮箱地址');
            return;
        }

        setIsSendingCode(true);
        try {
            await sendCode(normalizedEmail);
            setCountdown(60);
        } catch (error: any) {
            toast.error(error?.message || '发送失败，请稍后重试');
        } finally {
            setIsSendingCode(false);
        }
    };

    const handleLogin = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!checked) {
            toast.error('请先同意用户协议和隐私政策');
            return;
        }
        if (!email || !code) {
            toast.error('请输入邮箱和验证码');
            return;
        }

        setIsLoading(true);
        try {
            await login(email.trim(), code.trim());
        } finally {
            setIsLoading(false);
        }
    };

    return (
        <div className="absolute inset-0 z-[100] flex flex-col items-center justify-center bg-black/30 backdrop-blur-sm p-4">
            <div className="relative w-full max-w-[320px] bg-white/90 dark:bg-zinc-900/90 backdrop-blur-xl border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-2xl p-6 space-y-6 transition-all duration-500 animate-in fade-in zoom-in-95">

                <button
                    type="button"
                    onClick={() => {
                        const win = window as any;
                        if (win.electron && win.electron.closeWindow) {
                            win.electron.closeWindow();
                        } else {
                            console.warn('Electron closeWindow API not available');
                        }
                    }}
                    className="absolute top-4 right-4 p-1 rounded-full text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
                >
                    <X className="h-4 w-4" />
                </button>

                <div className="text-center space-y-1">
                    <div className="mx-auto h-16 w-16 flex items-center justify-center mb-4">
                        <Logo className="drop-shadow-xl" />
                    </div>
                    <h1 className="text-xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">欢迎回来</h1>
                    <p className="text-xs text-zinc-500 dark:text-zinc-400">登录 XiaoYing 账号以继续</p>
                </div>

                <div className="space-y-4 transition-all duration-300 ease-in-out">
                    <form onSubmit={handleLogin} className="space-y-4">

                        <div className="space-y-1">
                            <div className="relative group">
                                <Mail className="absolute left-3 top-2.5 h-4 w-4 text-zinc-400 group-focus-within:text-purple-500 transition-colors" />
                                <input
                                    type="email"
                                    placeholder="邮箱地址"
                                    value={email}
                                    onChange={e => setEmail(e.target.value)}
                                    className="w-full pl-9 pr-3 py-2 bg-zinc-50 dark:bg-zinc-800/50 border border-zinc-200 dark:border-zinc-700 rounded-lg text-sm text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-purple-500/50 focus:border-purple-500 transition-all placeholder:text-zinc-400"
                                />
                            </div>
                        </div>

                        <div className="space-y-1">
                            <div className="relative group flex items-center gap-2">
                                <div className="relative flex-1 group">
                                    <Key className="absolute left-3 top-2.5 h-4 w-4 text-zinc-400 group-focus-within:text-purple-500 transition-colors" />
                                    <input
                                        type="text"
                                        placeholder="验证码"
                                        value={code}
                                        onChange={e => setCode(e.target.value)}
                                        className="w-full pl-9 pr-3 py-2 bg-zinc-50 dark:bg-zinc-800/50 border border-zinc-200 dark:border-zinc-700 rounded-lg text-sm text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-purple-500/50 focus:border-purple-500 transition-all placeholder:text-zinc-400"
                                    />
                                </div>
                                <button
                                    type="button"
                                    onClick={handleSendCode}
                                    disabled={countdown > 0 || isSendingCode}
                                    className={cn(
                                        "px-3 py-2 text-xs font-medium rounded-lg min-w-[80px] transition-all duration-200 flex items-center justify-center whitespace-nowrap",
                                        countdown > 0 || isSendingCode
                                            ? "bg-zinc-100 dark:bg-zinc-800 text-zinc-400 cursor-not-allowed"
                                            : "bg-purple-600 text-white hover:bg-purple-700 shadow-sm hover:shadow-purple-500/20 active:scale-95"
                                    )}
                                >
                                    {isSendingCode ? (
                                        <Loader2 className="h-3 w-3 animate-spin" />
                                    ) : countdown > 0 ? (
                                        `${countdown}s`
                                    ) : (
                                        '获取'
                                    )}
                                </button>
                            </div>
                        </div>

                        <div className="flex items-center space-x-2 pt-1">
                            <button
                                type="button"
                                onClick={() => setChecked(!checked)}
                                className={cn(
                                    "h-4 w-4 rounded border flex items-center justify-center transition-all duration-200",
                                    checked
                                        ? "bg-purple-600 border-purple-600 text-white"
                                        : "bg-transparent border-zinc-300 dark:border-zinc-600 text-transparent hover:border-purple-400"
                                )}
                            >
                                <Check className="h-3 w-3" strokeWidth={3} />
                            </button>
                            <span className="text-[10px] text-zinc-500 dark:text-zinc-400 leading-tight">
                                我已阅读并同意
                                <a href="https://www.auramate.cn/terms-of-service/" target="_blank" rel="noopener noreferrer" className="text-purple-600 hover:underline ml-1">用户服务协议</a>
                                {' & '}
                                <a href="https://www.auramate.cn/privacy-policy/" target="_blank" rel="noopener noreferrer" className="text-purple-600 hover:underline">隐私政策</a>
                            </span>
                        </div>

                        <button
                            type="submit"
                            disabled={isLoading}
                            className="w-full py-2.5 bg-gradient-to-r from-purple-600 to-indigo-600 text-white rounded-lg text-sm font-semibold hover:from-purple-700 hover:to-indigo-700 transition-all duration-200 shadow-lg shadow-purple-500/20 disabled:opacity-70 disabled:cursor-not-allowed disabled:shadow-none flex items-center justify-center gap-2 active:scale-[0.98]"
                        >
                            {isLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : '登录'}
                        </button>
                    </form>
                </div>
            </div>

            <div className="mt-8 text-[10px] text-zinc-400/50 text-center">
                XiaoYing Desktop
            </div>
        </div>
    );
};
