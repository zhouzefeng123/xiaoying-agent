import { useState, useEffect, useRef, type CSSProperties } from 'react';
import { cn } from '../../lib/utils';
import { toast } from 'sonner';
import { Zap } from 'lucide-react';
import type { Thought } from '../../hooks/use-chat';
import logo from '../../assets/workbuddy-logo.svg';

interface CognitiveState {
    state: string;
    token_usage: number;
    token_limit: number;
    remaining_tokens: number;
    points_left: number;
}

interface AuraStatusButtonProps {
    thoughts?: Thought[];
}

function normalizeCognitiveState(data: unknown): CognitiveState | null {
    if (!data || typeof data !== 'object') {
        return null;
    }

    const raw = data as Partial<CognitiveState>;
    const toSafeNumber = (value: unknown) => (
        typeof value === 'number' && Number.isFinite(value) ? value : 0
    );

    return {
        state: typeof raw.state === 'string' && raw.state.trim() !== '' ? raw.state : 'dormant',
        token_usage: toSafeNumber(raw.token_usage),
        token_limit: toSafeNumber(raw.token_limit),
        remaining_tokens: toSafeNumber(raw.remaining_tokens),
        points_left: toSafeNumber(raw.points_left),
    };
}

export function AuraStatusButton({ thoughts = [] }: AuraStatusButtonProps) {
    const [status, setStatus] = useState<CognitiveState | null>(null);
    const [liveState, setLiveState] = useState<string>('dormant');
    const prevStatusRef = useRef<string>('dormant');
    const hasWelcomedRef = useRef<boolean>(false);
    const [isToggling, setIsToggling] = useState(false);

    const formatNumber = (value: number | null | undefined) => (
        typeof value === 'number' && Number.isFinite(value)
            ? value.toLocaleString('zh-CN')
            : '0'
    );

    // Polling for state
    useEffect(() => {
        const fetchState = async () => {
            try {
                const res = await fetch('/v1/cognitive/state');
                if (res.ok) {
                    const nextStatus = normalizeCognitiveState(await res.json());
                    if (!nextStatus) {
                        return;
                    }

                    setStatus(nextStatus);
                    setLiveState(nextStatus.state);

                    // Status is reflected visually by the button, no toast needed
                    // if (data.state === 'interactive' && !hasWelcomedRef.current) {
                    //     toast.success('小A已上线，随时待命！', {
                    //         description: 'Aura is online and ready to help.',
                    //         duration: 1500,
                    //     });
                    //     hasWelcomedRef.current = true;
                    // }
                    
                    prevStatusRef.current = nextStatus.state;
                }
            } catch {
                return;
            }
        };

        fetchState();
        const interval = setInterval(fetchState, 2000); // Poll every 2s for faster updates (was 5s)
        return () => clearInterval(interval);
    }, []);

    useEffect(() => {
        if (thoughts.length === 0) {
            return;
        }
        const last = thoughts[thoughts.length - 1];
        if (last.type === 'state_change') {
            setLiveState(last.content);
        }
    }, [thoughts]);

    const toggleState = async () => {
        if (!status) return;
        setIsToggling(true);
        // If current state is dormant, we want to activate.
        // If current state is anything else (including standby, interactive, etc.), we want to go dormant.
        const targetState = status.state === 'dormant' ? 'active' : 'dormant';
        
        try {
            const res = await fetch('/v1/cognitive/switch', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ target_state: targetState })
            });
            
            if (res.ok) {
                // Status change is reflected visually by the button, no toast needed
                // toast.success(targetState === 'active' ? '小A启动中...' : '小A正在休眠...');
            } else {
                toast.error('切换状态失败');
            }
        } catch {
            toast.error('网络错误');
        } finally {
            setIsToggling(false);
        }
    };

    // State visuals are applied to the logo itself so the top-left mark stays clean.
    const getStateGlow = (state: string) => {
        switch (state) {
            case 'waking': return 'brightness-110';
            case 'interactive': return 'saturate-125';
            case 'standby': return 'saturate-110';
            case 'reflective': return 'saturate-125';
            case 'exploratory': return 'saturate-125';
            case 'dreaming': return 'saturate-125';
            case 'dormant': return 'opacity-80 grayscale';
            default: return 'opacity-80 grayscale';
        }
    };

    const getStateLogoStyle = (state: string): CSSProperties => {
        switch (state) {
            case 'waking':
                return {
                    filter: 'drop-shadow(0 0 6px rgba(255,255,255,0.85)) drop-shadow(0 0 16px rgba(255,255,255,0.55)) brightness(1.12)'
                };
            case 'interactive':
                return {
                    filter: 'drop-shadow(0 0 6px rgba(16,185,129,0.95)) drop-shadow(0 0 18px rgba(16,185,129,0.7)) saturate(1.28) brightness(1.05)'
                };
            case 'standby':
                return {
                    filter: 'drop-shadow(0 0 5px rgba(236,72,153,0.75)) drop-shadow(0 0 14px rgba(236,72,153,0.45)) saturate(1.15)'
                };
            case 'reflective':
                return {
                    filter: 'drop-shadow(0 0 6px rgba(168,85,247,0.95)) drop-shadow(0 0 18px rgba(168,85,247,0.7)) saturate(1.28) brightness(1.05)'
                };
            case 'exploratory':
                return {
                    filter: 'drop-shadow(0 0 6px rgba(234,179,8,0.95)) drop-shadow(0 0 18px rgba(234,179,8,0.65)) saturate(1.28) brightness(1.05)'
                };
            case 'dreaming':
                return {
                    filter: 'drop-shadow(0 0 7px rgba(99,102,241,1)) drop-shadow(0 0 20px rgba(99,102,241,0.75)) saturate(1.28) brightness(1.06)'
                };
            case 'dormant':
            default:
                return {
                    filter: 'grayscale(1) opacity(0.8)'
                };
        }
    };

    const getStateLabel = (state: string) => {
        switch (state) {
            case 'waking': return '苏醒中... (Waking)';
            case 'interactive': return '在线 (Interactive)';
            case 'standby': return '待机 (Standby)';
            case 'reflective': return '反思中 (Reflective)';
            case 'exploratory': return '探索中 (Exploratory)';
            case 'dreaming': return '造梦中 (Dreaming)';
            case 'dormant': return '休眠 (Dormant)';
            default: return state;
        }
    };

    const getStateDescription = (state: string) => {
        switch (state) {
            case 'waking': return '系统正在初始化...';
            case 'interactive': return '"Life Mode" 激活。';
            case 'reflective': return '内部思维处理，自我优化。';
            case 'exploratory': return '主动信息搜集与环境探索。';
            case 'dreaming': return '检测到记忆碎片后触发。进行深度整理与知识固化。';
            case 'dormant': return '工具模式：高效执行，支持记忆调用';
            default: return '';
        }
    };

    const currentState = liveState || status?.state || 'dormant';

    return (
        <div className="relative group cursor-pointer" onClick={toggleState}>
            <div className={cn(
                "w-10 h-10 flex items-center justify-center transition-all duration-500",
                "hover:scale-110",
                isToggling && "animate-energetic-bounce"
            )}>
                <img 
                    src={logo}
                    alt="小英" 
                    className={cn(
                        "w-8 h-8 object-contain transition-all duration-500 select-none",
                        getStateGlow(currentState)
                    )} 
                    style={getStateLogoStyle(currentState)}
                />
            </div>

            {/* Hover Tooltip */}
            <div className="absolute top-full left-0 mt-2 w-64 p-3 rounded-lg bg-black/80 backdrop-blur-md border border-white/10 text-white text-xs opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-50 shadow-xl">
                <div className="flex items-center justify-between mb-2">
                    <div className="font-bold text-sm flex items-center gap-2">
                        <span className={cn(
                            "w-2 h-2 rounded-full", 
                            currentState === 'dormant' ? "bg-zinc-500" : 
                            currentState === 'interactive' ? "bg-emerald-500 animate-pulse" :
                            currentState === 'reflective' ? "bg-purple-500 animate-pulse" :
                            currentState === 'exploratory' ? "bg-yellow-500 animate-pulse" :
                            currentState === 'dreaming' ? "bg-indigo-500 animate-pulse" :
                            "bg-white animate-pulse"
                        )} />
                        {getStateLabel(currentState)}
                    </div>
                </div>
                
                <div className="mb-3 text-zinc-400 leading-relaxed">
                    {getStateDescription(currentState)}
                </div>
                
                {status && (
                    <div className="space-y-2 text-zinc-300 border-t border-white/10 pt-2">
                        <div className="flex justify-between items-center">
                            <span className="flex items-center gap-1.5 text-[10px] tracking-wider text-zinc-500">
                                <Zap className="w-3 h-3" />
                                Token 用量
                            </span>
                            <span className="font-mono text-[10px]">{formatNumber(status.token_usage)} / {formatNumber(status.token_limit)}</span>
                        </div>
                        <div className="flex justify-between items-center text-[10px] text-zinc-400">
                            <span>剩余</span>
                            <span className="font-mono">{formatNumber(status.remaining_tokens)}</span>
                        </div>
                        <div className="w-full h-1 bg-white/10 rounded-full overflow-hidden">
                            <div 
                                className="h-full bg-blue-500 transition-all duration-500"
                                style={{ width: `${status.token_limit > 0 ? Math.min((status.token_usage / status.token_limit) * 100, 100) : 0}%` }}
                            />
                        </div>
                    </div>
                )}
                
                <div className="mt-2 text-[10px] text-zinc-600 text-center pt-1">
                    {currentState === 'dormant' ? '点击激活 Life Mode' : '点击切换至休眠模式'}
                </div>
            </div>
        </div>
    );
}
