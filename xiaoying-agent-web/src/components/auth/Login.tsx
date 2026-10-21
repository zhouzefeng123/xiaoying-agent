import React, { useEffect, useState } from 'react';
import { useAuth } from '../../contexts/useAuth';
import { toast } from 'sonner';
import { ArrowRight, Check, KeyRound, Loader2, Mail, X } from 'lucide-react';
import { Logo } from '../common/Logo';
import { Robot3D } from './Robot3D';
import './Login.css';

const resolveErrorMessage = (error: unknown, fallback: string) => (
    error instanceof Error && error.message ? error.message : fallback
);

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

    const canCloseWindow = typeof window !== 'undefined' && Boolean(window.electron?.closeWindow);

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
        } catch (error: unknown) {
            toast.error(resolveErrorMessage(error, '发送失败，请稍后重试'));
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
        <div className="login-screen">
            {canCloseWindow && (
                <button
                    type="button"
                    className="login-window-close"
                    onClick={() => window.electron?.closeWindow()}
                    aria-label="关闭窗口"
                    title="关闭窗口"
                >
                    <X className="h-4 w-4" />
                </button>
            )}

            <div className="login-page-grid">
                <section className="login-visual-panel" aria-label="小英机器人">
                    <div className="login-brand">
                        <span className="login-brand-mark">
                            <Logo />
                        </span>
                        <span className="login-brand-copy">
                            <strong>XIAOYING</strong>
                            <span>智能工作伙伴</span>
                        </span>
                    </div>

                    <div className="login-visual-copy">
                        <p className="login-eyebrow">YOUR EVERYDAY AI PARTNER</p>
                        <h1>你好，<span>我是小英！</span></h1>
                        <p>把注意力留给真正重要的事，复杂的部分交给我。</p>
                    </div>

                    <div className="login-robot-stage">
                        <Robot3D />
                        <div className="login-robot-status">奶蛙模式 · 在线</div>
                        <div className="login-robot-note">
                            <strong>今天想完成什么？</strong>
                            我已经准备好了，随时可以开始。
                        </div>
                    </div>

                    <div className="login-visual-footer" aria-hidden="true">
                        <span>FOCUS</span>
                        <span>CREATE</span>
                        <span>SHIP</span>
                    </div>
                </section>

                <section className="login-form-panel" aria-labelledby="login-heading">
                    <div className="login-form-card">
                        <div className="login-form-heading">
                            <span className="login-form-kicker">WELCOME BACK</span>
                            <h2 id="login-heading">欢迎回来</h2>
                            <p>登录 XiaoYing，继续今天的工作。</p>
                        </div>

                        <form className="login-form" onSubmit={handleLogin}>
                            <div className="login-field">
                                <label className="login-field-label" htmlFor="login-email">邮箱地址</label>
                                <div className="login-input-shell">
                                    <Mail className="login-input-icon" />
                                    <input
                                        id="login-email"
                                        type="email"
                                        placeholder="name@company.com"
                                        value={email}
                                        onChange={e => setEmail(e.target.value)}
                                        className="login-input"
                                        autoComplete="email"
                                    />
                                </div>
                            </div>

                            <div className="login-field">
                                <label className="login-field-label" htmlFor="login-code">验证码</label>
                                <div className="login-code-row">
                                    <div className="login-input-shell">
                                        <KeyRound className="login-input-icon" />
                                        <input
                                            id="login-code"
                                            type="text"
                                            inputMode="numeric"
                                            maxLength={6}
                                            placeholder="输入 6 位验证码"
                                            value={code}
                                            onChange={e => setCode(e.target.value)}
                                            className="login-input"
                                            autoComplete="one-time-code"
                                        />
                                    </div>
                                    <button
                                        type="button"
                                        onClick={handleSendCode}
                                        disabled={countdown > 0 || isSendingCode}
                                        className="login-code-button"
                                    >
                                        {isSendingCode ? (
                                            <Loader2 className="h-4 w-4 animate-spin" />
                                        ) : countdown > 0 ? (
                                            `${countdown}s`
                                        ) : (
                                            '获取验证码'
                                        )}
                                    </button>
                                </div>
                            </div>

                            <label className="login-terms">
                                <input
                                    type="checkbox"
                                    checked={checked}
                                    onChange={e => setChecked(e.target.checked)}
                                />
                                <span className="login-checkmark">
                                    <Check className="h-3 w-3" strokeWidth={3} />
                                </span>
                                <span>
                                    我已阅读并同意
                                    <a
                                        href="https://www.auramate.cn/terms-of-service/"
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        onClick={e => e.stopPropagation()}
                                    >
                                        用户服务协议
                                    </a>
                                    和
                                    <a
                                        href="https://www.auramate.cn/privacy-policy/"
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        onClick={e => e.stopPropagation()}
                                    >
                                        隐私政策
                                    </a>
                                </span>
                            </label>

                            <button type="submit" disabled={isLoading} className="login-submit">
                                <span>{isLoading ? '正在登录…' : '进入工作台'}</span>
                                <span className="login-submit-arrow">
                                    {isLoading ? (
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                    ) : (
                                        <ArrowRight className="h-4 w-4" />
                                    )}
                                </span>
                            </button>
                        </form>

                        <p className="login-form-footnote">
                            首次登录将自动创建账号 · <span>安全加密连接</span>
                        </p>
                    </div>
                </section>
            </div>
        </div>
    );
};
