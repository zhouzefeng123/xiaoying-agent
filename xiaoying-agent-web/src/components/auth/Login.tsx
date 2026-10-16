import React, { useState, useEffect, useRef } from 'react';
import { useAuth } from '../../contexts/useAuth';
import { RegistryCaptcha, UnmountCaptcha } from '../../common/captcha';
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
    const [captchaReady, setCaptchaReady] = useState(false);
    
    // Ref for email to access in captcha callback closure
    const emailRef = useRef(email);
    useEffect(() => { emailRef.current = email; }, [email]);

    const captchaInitialized = useRef(false);
    const captchaInstance = useRef<any>(null);
    const [scriptError, setScriptError] = useState(false);

    // Effect: Resize window for login - REMOVED based on user feedback
    // User prefers default window behavior or centering without forced resize
    /*
    useEffect(() => {
        const electron = (window as any).electron;
        if (electron && electron.resizeWindow) {
            electron.resizeWindow(400, 500);
        }
    }, []);
    */

    const loadCaptchaScript = () => {
        return new Promise<void>((resolve, reject) => {
            if ((window as any).initAliyunCaptcha) {
                resolve();
                return;
            }
            const script = document.createElement('script');
            script.src = 'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js';
            script.async = true;
            script.onload = () => resolve();
            script.onerror = () => reject(new Error('Failed to load captcha script'));
            document.head.appendChild(script);
        });
    };

    useEffect(() => {
        if (countdown <= 0) return;
        const timer = setInterval(() => setCountdown(c => c - 1), 1000);
        return () => clearInterval(timer);
    }, [countdown]);

    useEffect(() => {
        let mounted = true;

        const initCaptcha = async () => {
            if (!mounted) return;
            if (captchaInitialized.current) return;

            try {
                setScriptError(false);
                await loadCaptchaScript();
                
                if (!mounted) return;

                // Check if SDK is loaded
                const win = window as any;
                if (!win.initAliyunCaptcha) {
                    throw new Error('Captcha SDK loaded but init function missing');
                }

                const btn = document.getElementById('send-code-btn');
                const element = document.getElementById('captcha-element');

                if (btn && element) {
                    RegistryCaptcha(
                        'send-code-btn',
                        'captcha-element',
                        async (params) => {
                            const currentEmail = emailRef.current;
                            if (!currentEmail) {
                                toast.error('请输入邮箱地址');
                                return { captchaResult: false, bizResult: false };
                            }
                            
                            // Email validation regex
                            const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
                            if (!emailRegex.test(currentEmail)) {
                                toast.error('请输入有效的邮箱地址');
                                return { captchaResult: false, bizResult: false };
                            }
    
                            try {
                                setIsSendingCode(true);
                                // Pass params directly, do not stringify
                                console.log('Captcha verify param:', params); // Log for debugging
                                await sendCode(currentEmail, params);
                                console.log('Send code success'); // Log success
                                setCountdown(60);
                                // Always return true for bizResult if sendCode didn't throw
                                // The actual business result (sending email) is async but handled by backend
                                return { captchaResult: true, bizResult: true };
                            } catch (e: any) {
                                // Error toast is handled here
                                console.error('Send code failed:', e);
                                toast.error(e.message || '发送失败');
                                // Even if sending code failed, captcha verification passed if we got here?
                                // No, if sendCode failed, it might be due to captcha verification failure on backend.
                                // But if it was a network error or email error, the captcha itself was valid?
                                // Aliyun docs say: "如无业务验证场景时，bizResult为可选"
                                // If we return false, the captcha might reset.
                                return { captchaResult: false, bizResult: false };
                            } finally {
                                setIsSendingCode(false);
                            }
                        },
                        (instance) => {
                            // Instance captured
                            captchaInitialized.current = true;
                            captchaInstance.current = instance;
                            if (mounted) setCaptchaReady(true);
                        }
                    );
                } else {
                    // DOM not ready, retry shortly? No, useEffect runs after render so DOM should be ready.
                    console.error('Captcha DOM elements not found');
                }
            } catch (err) {
                console.error("Failed to load captcha script", err);
                if (mounted) {
                    setScriptError(true);
                    toast.error('验证码组件加载失败，请检查网络');
                }
            }
        };

        initCaptcha();

        return () => {
            if (captchaInitialized.current) {
                UnmountCaptcha();
                captchaInitialized.current = false;
            }
            mounted = false;
        }
    }, [sendCode, scriptError]); // Retry when scriptError changes (if triggered by retry button)

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
            await login(email, code);
        } catch (e) {
            // Error toast handled in login
        } finally {
            setIsLoading(false);
        }
    };

    return (
        <div className="absolute inset-0 z-[100] flex flex-col items-center justify-center bg-black/30 backdrop-blur-sm p-4">
            {/* Card Container - constrained width for small window */}
            <div className="relative w-full max-w-[320px] bg-white/90 dark:bg-zinc-900/90 backdrop-blur-xl border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-2xl p-6 space-y-6 transition-all duration-500 animate-in fade-in zoom-in-95">
                
                {/* Close Button */}
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

                {/* Header */}
                <div className="text-center space-y-1">
                    <div className="mx-auto h-16 w-16 flex items-center justify-center mb-4">
                        <Logo className="drop-shadow-xl" />
                    </div>
                    <h1 className="text-xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">欢迎回来</h1>
                    <p className="text-xs text-zinc-500 dark:text-zinc-400">登录 XiaoYing 账号以继续</p>
                </div>
                
                {/* Sliding Form Container */}
                <div className="space-y-4 transition-all duration-300 ease-in-out">
                     <form onSubmit={handleLogin} className="space-y-4">
                     
                     {/* Email Input */}
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
 
                     {/* Code Input + Button */}
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
                                id="send-code-btn"
                                disabled={(countdown > 0 || isSendingCode || !captchaReady) && !scriptError}
                                onClick={(e) => {
                                    if (scriptError) {
                                        setScriptError(false);
                                        return;
                                    }
                                    // Manually trigger captcha show to avoid binding issues
                                    if (captchaInstance.current?.show) {
                                        captchaInstance.current.show();
                                    }
                                }}
                                className={cn(
                                    "px-3 py-2 text-xs font-medium rounded-lg min-w-[80px] transition-all duration-200 flex items-center justify-center whitespace-nowrap",
                                    (countdown > 0 || (!captchaReady && !scriptError))
                                        ? "bg-zinc-100 dark:bg-zinc-800 text-zinc-400 cursor-not-allowed"
                                        : "bg-purple-600 text-white hover:bg-purple-700 shadow-sm hover:shadow-purple-500/20 active:scale-95"
                                )}
                            >
                                {isSendingCode ? (
                                    <Loader2 className="h-3 w-3 animate-spin" />
                                ) : countdown > 0 ? (
                                    `${countdown}s`
                                ) : !captchaReady ? (
                                    scriptError ? "重试" : <Loader2 className="h-3 w-3 animate-spin text-zinc-400" />
                                ) : (
                                    "获取"
                                )}
                            </button>
                        </div>
                        {/* Hidden captcha element anchor */}
                        <div id="captcha-element" className="absolute"></div>
                    </div>
 
                     {/* Agreement Checkbox */}
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
     
                         {/* Submit Button */}
                         <button
                             type="submit"
                             disabled={isLoading}
                             className="w-full py-2.5 bg-gradient-to-r from-purple-600 to-indigo-600 text-white rounded-lg text-sm font-semibold hover:from-purple-700 hover:to-indigo-700 transition-all duration-200 shadow-lg shadow-purple-500/20 disabled:opacity-70 disabled:cursor-not-allowed disabled:shadow-none flex items-center justify-center gap-2 active:scale-[0.98]"
                         >
                             {isLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : "登 录"}
                         </button>
                     </form>
                </div>
            </div>
            
            {/* Version Info */}
            <div className="mt-8 text-[10px] text-zinc-400/50 text-center">
                XiaoYing Desktop
            </div>
        </div>
    );
};
