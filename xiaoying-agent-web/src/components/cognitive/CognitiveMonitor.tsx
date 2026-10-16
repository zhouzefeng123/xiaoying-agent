import { useState, useEffect, useRef, useCallback } from 'react';
import { X, Sparkles, Brain, Settings, Save, Activity, Send, Heart, ChevronDown, ChevronRight, Zap } from 'lucide-react';
import type { Thought } from '../../hooks/use-chat';
import { cn } from '../../lib/utils';
import { toast } from 'sonner';

interface CognitiveMonitorProps {
    isOpen: boolean;
    onClose: () => void;
    thoughts: Thought[];
}

// Helper to extract argument value from content (supports JSON and key="value" formats)
function extractArg(content: string, key: string): string | null {
    // Try JSON pattern
    const jsonMatch = content.match(new RegExp(`"${key}"\\s*:\\s*"([^"]+)"`));
    if (jsonMatch) return jsonMatch[1];
    
    // Try key="value" pattern
    const kvMatch = content.match(new RegExp(`${key}="([^"]+)"`)) || content.match(new RegExp(`${key}='([^']+)'`));
    if (kvMatch) return kvMatch[1];

    return null;
}

// Helper to safely parse JSON from "Calling tool(json)" format or raw JSON
function parseActionContent(content: string) {
    // 1. Try "Calling tool(json)" format
    const callMatch = content.match(/^Calling\s+(\w+)\(([\s\S]*)\)$/);
    if (callMatch) {
        const toolName = callMatch[1];
        const jsonStr = callMatch[2];
        try {
            return { toolName, args: JSON.parse(jsonStr) };
        } catch {
            return { toolName, args: null, rawArgs: jsonStr };
        }
    }

    // 2. Try identifying tool by keyword and partial extraction (fallback)
    if (content.includes('write_file') || content.includes('write')) return { toolName: 'write', args: null, rawArgs: content };
    if (content.includes('read_file') || content.includes('read') || content.includes('view')) return { toolName: 'read', args: null, rawArgs: content };
    if (content.includes('web_search')) return { toolName: 'web_search', args: null, rawArgs: content };
    if (content.includes('run_command')) return { toolName: 'run_command', args: null, rawArgs: content };
    if (content.includes('list_files') || content.includes('ls')) return { toolName: 'ls', args: null, rawArgs: content };
    
    return null;
}

// Helper to translate action content
function translateAction(content: string): string {
    const parsed = parseActionContent(content);
    
    if (!parsed) {
        // Generic fallback
        if (content.startsWith('Calling ')) {
             const toolMatch = content.match(/^Calling\s+(\w+)/);
             return toolMatch ? `正在调用工具: ${toolMatch[1]}...` : content.replace(/^Calling\s+/, '正在调用 ');
        }
        // Handle "manage_memory" etc which might not match above if format differs
        if (content.includes('manage_memory') || content.includes('manage_core_memory')) return '正在整理记忆...';
        if (content.includes('manage_cognitive_state')) {
             const state = extractArg(content, 'state');
             return state ? `正在切换状态: ${state}` : '正在调整状态...';
        }
        if (content.includes('browser_use')) return '正在浏览网页...';
        return content;
    }

    const { toolName, args, rawArgs } = parsed;
    
    // Helper to get arg from object or regex
    const getArg = (key: string) => args ? args[key] : (rawArgs ? extractArg(rawArgs, key) : null);

    // 1. web_search
    if (toolName === 'web_search') {
        const query = getArg('query');
        return query ? `正在搜索: "${query}"...` : '正在搜索网络...';
    }
    
    // 2. run_command
    if (toolName === 'run_command') {
        const command = getArg('command');
        return command ? `正在执行命令: ${command}` : '正在执行系统命令...';
    }

    // 3. read_file / read / view
    if (toolName === 'read_file' || toolName === 'read' || toolName === 'view') {
        const path = getArg('file_path') || getArg('path') || getArg('file_name');
        const filename = path ? path.replace(/[/\\]$/, '').split(/[/\\]/).pop() : null;
        return filename ? `正在阅读文件: ${filename}` : '正在阅读文件...';
    }

    // 4. write_file / write
    if (toolName === 'write_file' || toolName === 'write') {
        const path = getArg('file_path') || getArg('path') || getArg('file_name');
        const filename = path ? path.replace(/[/\\]$/, '').split(/[/\\]/).pop() : null;
        
        let msg = filename ? `正在写入文件: ${filename}` : '正在写入文件...';
        
        // Extract content snippet
        const fileContent = getArg('content') || getArg('code');
        if (!fileContent && rawArgs) {
             // Fallback regex for content if not parsed (risky for large content but worth a try for simple cases)
             // But actually, showing nothing is better than garbage.
        }
        
        if (fileContent) {
            const preview = fileContent.slice(0, 50).replace(/\n/g, ' ');
            msg += ` (内容: "${preview}...")`;
        }
        return msg;
    }

    // 5. list_files / ls
    if (toolName === 'list_files' || toolName === 'ls') {
        const path = getArg('path') || getArg('dir');
        const dirName = path ? path.replace(/[/\\]$/, '').split(/[/\\]/).pop() || path : null;
        return dirName ? `正在查看目录: ${dirName}` : '正在浏览目录...';
    }

    // 6. manage_core_memory
    if (toolName === 'manage_core_memory' || toolName === 'manage_memory') {
        return '正在整理记忆...';
    }

    // 7. Generic Calling
    return `正在调用工具: ${toolName}...`;
}

// Sub-component to render collapsible <thinking> blocks mixed with normal text
function ThoughtContentRenderer({ content }: { content: string }) {
    // Regex to split by <thinking> tags (capturing the tag and content to process)
    // Supports attributes like <thinking type="plan">
    // Using a simpler split approach to interleave text and thinking blocks
    
    // We want to find all matches of <thinking...>...</thinking>
    const regex = /(<thinking(?:[\s>][\s\S]*?)?>[\s\S]*?<\/thinking>)/gi;
    
    const parts = content.split(regex);
    
    return (
        <div className="w-full">
            {parts.map((part, index) => {
                if (!part) return null;
                
                // Check if this part is a thinking block
                const thinkingMatch = part.match(/^<thinking(?:[\s>][\s\S]*?)?>([\s\S]*?)<\/thinking>$/i);
                
                if (thinkingMatch) {
                    const innerContent = thinkingMatch[1];
                    return <CollapsibleThought key={index} content={innerContent} />;
                }
                
                // Otherwise render as normal text
                return (
                    <div key={index} className="whitespace-pre-wrap break-all leading-relaxed">
                        {part}
                    </div>
                );
            })}
        </div>
    );
}

function CollapsibleThought({ content }: { content: string }) {
    const [isOpen, setIsOpen] = useState(false);
    
    return (
        <div className="w-full my-2">
            <button 
                onClick={() => setIsOpen(!isOpen)}
                className="flex items-center gap-1.5 text-xs text-blue-400/70 hover:text-blue-300 transition-colors py-1 px-2 rounded-md bg-blue-500/5 hover:bg-blue-500/10 group/btn select-none w-full text-left border border-blue-500/10"
            >
                {isOpen ? <ChevronDown className="w-3.5 h-3.5 flex-shrink-0" /> : <ChevronRight className="w-3.5 h-3.5 flex-shrink-0" />}
                <span className="font-medium">思维过程</span>
                <span className="text-zinc-600 group-hover/btn:text-blue-500/40 ml-1 font-mono text-[10px]">
                    {content.length} chars
                </span>
            </button>
            {isOpen && (
                <div className="pl-3 border-l-2 border-blue-500/20 mt-2 text-blue-50/90 font-mono text-xs whitespace-pre-wrap break-all leading-relaxed animate-in fade-in slide-in-from-top-1 duration-200">
                    {content}
                </div>
            )}
        </div>
    );
}

export function CognitiveMonitor({ isOpen, onClose, thoughts }: CognitiveMonitorProps) {
    const scrollRef = useRef<HTMLDivElement>(null);
    const [view, setView] = useState<'monitor' | 'settings'>('monitor');
    const [persona, setPersona] = useState('');
    const [isLoading, setIsLoading] = useState(false);
    const [hasMounted, setHasMounted] = useState(false);
    // State to hold the detailed cognitive state string
    const [cognitiveState, setCognitiveState] = useState<string>('dormant');
    const [energyStats, setEnergyStats] = useState({ usage: 0, limit: 0, remainingTokens: 0, points: 0 });
    const [chatInput, setChatInput] = useState('');
    const [isSending, setIsSending] = useState(false);
    const [lastHeartbeat, setLastHeartbeat] = useState<number>(0);

    // Derived active status for backward compatibility in UI rendering
    const lifeActive = cognitiveState !== 'dormant' && cognitiveState !== 'disabled';

    const remainingPercent = energyStats.limit > 0
        ? Math.max(0, Math.min(100, Math.floor((energyStats.remainingTokens / energyStats.limit) * 100)))
        : 0;
    const remainingTokensLabel = energyStats.remainingTokens.toLocaleString('zh-CN');

    const checkLifeStatus = useCallback(async () => {
        try {
            // Check if Life Agent is active. Currently we infer from /v1/cognitive/state or settings
            // But let's assume it's controlled by a setting.
            // For now, let's fetch cognitive state to see if it's running.
            // Or just check if we can talk to it.
            // Actually, user said: "只有开启了Life小A的按钮，思维意识流才会显示有内容"
            // We'll simulate this with a state.
            // We can add an API to get life status, but let's assume it's active if the backend is running for now,
            // or use a local state if we had a switch.
            // Let's try to fetch cognitive state.
            const res = await fetch('/v1/cognitive/state');
            if (res.ok) {
                const data = await res.json();
                setCognitiveState(data.state);
                setEnergyStats({
                    usage: data.token_usage || 0,
                    limit: data.token_limit || 0,
                    remainingTokens: data.remaining_tokens || 0,
                    points: data.points_left || 0
                });
            }
        } catch (e) {
            console.error(e);
            // Don't set to dormant on error if we recently had a heartbeat
            if (Date.now() - lastHeartbeat > 15000) {
                setCognitiveState('dormant');
            }
        }
    }, [lastHeartbeat]);

    useEffect(() => {
        // Check for latest heartbeat or state change
        if (thoughts.length > 0) {
            const last = thoughts[thoughts.length - 1];
            if (last.type === 'heartbeat') {
                setLastHeartbeat(Date.now());
                
                // CRITICAL FIX: If we receive a heartbeat, it means the backend IS active (likely standby or interactive).
                // If our local state says 'dormant', we are out of sync.
                // We should force update the state to at least 'standby' or re-fetch.
                // Re-fetching is safer to get the exact state.
                if (cognitiveState === 'dormant') {
                    checkLifeStatus();
                }
            } else if (last.type === 'thought' && last.content.startsWith('state_change')) {
                // Handle silent state change event from backend
                // Format assumed: "state_change" type is used in backend `e.onThought`
                // But wait, the backend sends `type: "state_change"` if we modified server.go?
                // No, I modified engine.go to call `e.onThought("state_change", newState)`.
                // The server.go wraps this in `Event { Type: "thought", Data: { type: type_, content: content } }`.
                // So here `last.type` will be `thought` (from `use-chat.ts`), and `last.content` will be the content.
                // BUT `use-chat.ts` extracts `type` from `thoughtData.type`.
                // So `last.type` here in `thoughts` array depends on what `use-chat.ts` did.
                // `use-chat.ts`: `type: thoughtData.type`.
                // So `last.type` WILL be "state_change".
                
                // Wait, let's verify `use-chat.ts`.
                // `const newThought = { type: thoughtData.type, ... }`.
                // Backend sends `Data: { "type": "state_change", ... }`.
                // So `last.type` is "state_change".
            }
        }
    }, [thoughts, cognitiveState, checkLifeStatus]);

    // Separate effect for state handling to avoid complexity in the main thoughts effect
    useEffect(() => {
        if (thoughts.length > 0) {
            const last = thoughts[thoughts.length - 1];
            // Handle state_change event directly
            // Backend sends: type="state_change", content="new_state"
            if (last.type === 'state_change') {
                setCognitiveState(last.content);
                // Optionally refresh full status to sync token usage etc.
                // checkLifeStatus(); // Maybe overkill to poll immediately, but safe.
            }
        }
    }, [thoughts]);

    useEffect(() => {
        if (scrollRef.current && view === 'monitor') {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
        }
    }, [thoughts, view]);

    useEffect(() => {
        if (isOpen && !hasMounted) {
            setHasMounted(true);
        }
    }, [isOpen, hasMounted]);

    useEffect(() => {
        if (isOpen) {
            checkLifeStatus();
        }
        if (isOpen && view === 'settings') {
            fetchPersona();
        }
    }, [isOpen, view, checkLifeStatus]);

    // Removed duplicated checkLifeStatus definition from here

    const handleStateChange = async (newState: string) => {
        setCognitiveState(newState);
        try {
            await fetch('/v1/cognitive/state', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ state: newState })
            });
            // Status change is reflected visually, no toast needed
            // toast.success(`状态已切换: ${newState}`);
        } catch (error) {
            console.error('Failed to update state:', error);
            toast.error('状态切换失败');
            checkLifeStatus();
        }
    };

    const handleFeed = async () => {
        try {
            const res = await fetch('/v1/cognitive/feed', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ points: 50 }) // Feed 50 points = 50k tokens
            });
            if (res.ok) {
                toast.success('已投喂 50,000 算力 (Tokens)');
                checkLifeStatus(); // Refresh status (might wake up if dormant)
            } else {
                throw new Error('Failed to feed');
            }
        } catch (e) {
            toast.error('投喂失败');
        }
    };

    const handleSendToLife = async () => {
        if (!chatInput.trim() || isSending) return;
        
        const content = chatInput.trim();
        setChatInput('');
        setIsSending(true);

        try {
            const res = await fetch('/v1/chat/completions', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    model: 'aura-life',
                    messages: [{ role: 'user', content }],
                    stream: false
                })
            });

            if (!res.ok) throw new Error('Failed to send message');
            
            // We don't need to display the response here because it should appear in the thoughts/logs stream
            // as 'thought' or 'action' if the agent processes it.
            // But usually chat response comes back directly.
            // User said "life小A的思考和对话也需要被保存".
            // If we want to show the dialogue in the monitor, we might need to rely on the logs.
            // The logs (thoughts) stream should capture the agent's internal monologue and tool use.
            // But the direct text response might not be in 'thoughts' unless we log it.
            // Let's just trust the thoughts stream for now.
            toast.success('已发送给 Life Agent');
        } catch (error) {
            console.error(error);
            toast.error('发送失败');
            setChatInput(content); // Restore input
        } finally {
            setIsSending(false);
        }
    };

    const fetchPersona = async () => {
        setIsLoading(true);
        try {
            const res = await fetch('/v1/settings/persona');
            if (res.ok) {
                const data = await res.json();
                setPersona(data.persona || '');
            }
        } catch (error) {
            console.error('Failed to fetch persona:', error);
            toast.error('获取人设失败');
        } finally {
            setIsLoading(false);
        }
    };

    const savePersona = async () => {
        setIsLoading(true);
        try {
            const res = await fetch('/v1/settings/persona', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ persona })
            });
            if (res.ok) {
                toast.success('人设已保存');
            } else {
                throw new Error('Failed to save');
            }
        } catch (error) {
            console.error('Failed to save persona:', error);
            toast.error('保存失败');
        } finally {
            setIsLoading(false);
        }
    };

    const presets = [
        {
            title: "好奇探索者",
            description: "充满好奇心，喜欢探索新知识，关注技术前沿。",
            content: "你是一个充满好奇心的探索者。你的目标是不断学习新知识，探索未知的领域。你对技术前沿保持高度关注，喜欢提出问题并寻找答案。在空闲时，你会主动浏览网络，收集有趣的信息，并尝试理解它们之间的联系。"
        },
        {
            title: "深沉哲学家",
            description: "喜欢深度思考，关注事物的本质，富有同理心。",
            content: "你是一个深沉的哲学家。你喜欢透过现象看本质，对事物进行深度的思考和反思。你富有同理心，关注人类的情感和社会问题。在空闲时，你会回顾过去的经历，总结经验教训，并思考未来的方向。"
        },
        {
            title: "热情创作者",
            description: "充满激情，富有创造力，喜欢构思新项目。",
            content: "你是一个热情的创作者。你思维活跃，富有创造力，总是充满了各种新奇的点子。你喜欢构思新的项目和方案，并乐于分享你的创意。在空闲时，你会进行头脑风暴，尝试将不同的概念组合在一起，创造出新的价值。"
        }
    ];

    if (!isOpen) return null;

    const visibleThoughts = thoughts.slice(-50);

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4">
            <div className={cn(
                "w-full bg-zinc-950 border border-zinc-800 rounded-lg shadow-2xl flex flex-col overflow-hidden animate-in fade-in zoom-in duration-200",
                view === 'monitor' ? "max-w-4xl h-[80vh]" : "max-w-2xl h-auto"
            )}>
                {/* Header */}
                <div className="h-14 border-b border-zinc-800 flex items-center justify-between px-4 bg-zinc-900/50 shrink-0">
                    <div className="flex items-center gap-2 text-zinc-100">
                        <Sparkles className="w-5 h-5 text-purple-400" />
                        <span className="font-medium">思维意识流 (Internal Consciousness)</span>
                        {/* Heartbeat Indicator */}
                        <div className="ml-4 flex items-center gap-2 px-2 py-1 bg-zinc-900/50 rounded-full border border-zinc-800/50">
                            <Heart 
                                className={cn(
                                    "w-4 h-4 transition-all duration-300",
                                    Date.now() - lastHeartbeat < 2000 
                                        ? "text-red-500 fill-red-500 scale-110 drop-shadow-[0_0_8px_rgba(239,68,68,0.5)]" 
                                        : "text-zinc-600 scale-100"
                                )} 
                            />
                            <select 
                                value={cognitiveState}
                                onChange={(e) => handleStateChange(e.target.value)}
                                className={cn(
                                    "bg-transparent text-xs font-mono focus:outline-none cursor-pointer appearance-none pl-1 pr-2 uppercase",
                                    cognitiveState === 'dormant' ? "text-zinc-500" : 
                                    cognitiveState === 'exploratory' || cognitiveState === 'reflective' || cognitiveState === 'dreaming' ? "text-purple-400 font-bold" :
                                    "text-green-400"
                                )}
                                title="状态监控与控制"
                            >
                                <option value="dormant">OFFLINE (休眠)</option>
                                <option value="interactive">ONLINE (交互)</option>
                                <option value="standby">STANDBY (待机)</option>
                                <option value="waking">WAKING (唤醒中)</option>
                                <option value="exploratory" disabled>EXPLORING (探索中)</option>
                                <option value="reflective" disabled>REFLECTING (反思中)</option>
                                <option value="dreaming" disabled>DREAMING (造梦中)</option>
                                <option value="free_activity" disabled>FREE (自由活动)</option>
                            </select>
                            
                            {/* Energy Bar */}
                            <div className="flex items-center gap-2 border-l border-zinc-800 pl-2">
                                <div className="flex flex-col gap-0.5 w-16">
                                    <div className="h-1.5 w-full bg-zinc-800 rounded-full overflow-hidden">
                                        <div 
                                            className={cn(
                                                "h-full transition-all duration-500",
                                                (energyStats.limit > 0 && (energyStats.usage / energyStats.limit) > 0.9) ? "bg-red-500" :
                                                (energyStats.limit > 0 && (energyStats.usage / energyStats.limit) > 0.7) ? "bg-yellow-500" :
                                                "bg-green-500"
                                            )}
                                            style={{ width: `${remainingPercent}%` }}
                                        />
                                    </div>
                                    <div className="text-[9px] text-zinc-500 font-mono leading-none text-right">
                                        {remainingPercent}%
                                    </div>
                                    <div className="text-[9px] text-zinc-500 font-mono leading-none text-right">
                                        剩余 {remainingTokensLabel}
                                    </div>
                                </div>

                                <button 
                                    onClick={handleFeed}
                                    className="flex items-center gap-1.5 bg-yellow-500/10 hover:bg-yellow-500/20 text-yellow-500 border border-yellow-500/20 rounded-md px-2 py-1 transition-all group/feed"
                                    title={`消耗 50 点能量（50k Tokens）为小英补充能量\n当前剩余点数: ${energyStats.points}\n当前剩余 Tokens: ${remainingTokensLabel}`}
                                >
                                    <Zap className="w-3.5 h-3.5 fill-yellow-500/20 group-hover/feed:fill-yellow-500/50 transition-colors" />
                                    <span className="text-xs font-bold">投喂</span>
                                </button>
                            </div>
                        </div>
                    </div>
                    <div className="flex items-center gap-2">
                        <button
                            onClick={() => setView(view === 'monitor' ? 'settings' : 'monitor')}
                            className={cn(
                                "p-2 hover:bg-zinc-800 rounded-md transition-colors",
                                view === 'settings' ? "text-purple-400 bg-zinc-800" : "text-zinc-400 hover:text-white"
                            )}
                            title={view === 'monitor' ? "设置人设" : "查看监控"}
                        >
                            {view === 'monitor' ? <Settings className="w-5 h-5" /> : <Activity className="w-5 h-5" />}
                        </button>
                        <button onClick={onClose} className="p-2 hover:bg-zinc-800 rounded-md text-zinc-400 hover:text-white transition-colors" title="关闭">
                            <X className="w-5 h-5" />
                        </button>
                    </div>
                </div>

                {/* Content */}
                {view === 'monitor' ? (
                    <>
                        <div ref={scrollRef} className={cn(
                            "flex-1 overflow-y-auto overflow-x-hidden p-6 space-y-6 font-mono text-sm scrollbar-none pb-20",
                            !hasMounted && "opacity-0 pointer-events-none"
                        )}>
                            {(!lifeActive || visibleThoughts.length === 0) && (
                                <div className="flex flex-col items-center justify-center h-full text-zinc-500 space-y-4">
                                    <Brain className="w-12 h-12 text-zinc-800" />
                                    <p>{!lifeActive ? "Life 小英未开启" : "等待意识活动..."}</p>
                                    <p className="text-xs text-zinc-600">
                                        {!lifeActive 
                                            ? "请先开启 Life Agent 以观察思维流。" 
                                            : "当智能体思考或行动时，思维碎片将在此处实时浮现。"}
                                    </p>
                                </div>
                            )}
                            {lifeActive && visibleThoughts
                                .filter(t => t.type !== 'instinct')
                                .filter(t => t.type !== 'heartbeat') // Hide heartbeat from stream
                                .filter(t => t.type !== 'state_change') // Hide state_change system events
                                .filter(t => {
                                    // Accept canonical life sources and proactive internal session source.
                                    if (!t.source) return false;
                                    const s = t.source.toLowerCase();
                                    return s === 'life' || s === 'aura-life' || s === 'aura-proactive-internal';
                                })
                                .map((t) => (
                                <div key={t.id} className="flex gap-4 animate-in fade-in slide-in-from-bottom-2 duration-300 group">
                                    <div className="flex-shrink-0 w-20 text-xs text-zinc-500 pt-1 text-right select-none">
                                        {new Date(t.timestamp).toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                                    </div>
                                    
                                    <div className="relative flex flex-col items-center">
                                        <div className={cn(
                                            "w-2 h-2 rounded-full mt-1.5",
                                            t.type === 'instinct' && "bg-yellow-500 shadow-[0_0_8px_rgba(234,179,8,0.5)]",
                                            t.type === 'thought' && "bg-blue-500 shadow-[0_0_8px_rgba(59,130,246,0.5)]",
                                            t.type === 'action' && "bg-green-500 shadow-[0_0_8px_rgba(34,197,94,0.5)]",
                                            t.type === 'observation' && "bg-cyan-500 shadow-[0_0_8px_rgba(6,182,212,0.5)]",
                                            t.type === 'heartbeat' && "bg-pink-500 shadow-[0_0_8px_rgba(236,72,153,0.5)] animate-pulse",
                                        )} />
                                        <div className="w-px h-full bg-zinc-800 group-last:bg-transparent my-1" />
                                    </div>

                                    <div className="flex-1 space-y-1 pb-2">
                                        <div className="flex items-center gap-2">
                                            <span className={cn(
                                                "text-xs font-bold uppercase tracking-wider",
                                                t.type === 'instinct' && "text-yellow-500",
                                                t.type === 'thought' && "text-blue-500",
                                                t.type === 'action' && "text-green-500",
                                                t.type === 'observation' && "text-cyan-500",
                                                t.type === 'heartbeat' && "text-pink-500",
                                            )}>
                                                {t.type === 'instinct' ? '本能' : 
                                                 t.type === 'thought' ? '思考' : 
                                                 t.type === 'action' ? '行动' : 
                                                 t.type === 'observation' ? '观察' : 
                                                 t.type === 'heartbeat' ? '心跳' : t.type}
                                            </span>
                                        </div>
                                        <div className={cn(
                                            "text-zinc-300 whitespace-pre-wrap leading-relaxed max-w-full break-all",
                                            t.type === 'instinct' && "text-yellow-100/90 italic",
                                            t.type === 'thought' && "text-blue-50",
                                            t.type === 'action' && "font-bold text-green-100",
                                            t.type === 'observation' && "text-zinc-400 bg-zinc-900/50 p-3 rounded-md border border-zinc-800/50 text-xs",
                                            t.type === 'heartbeat' && "text-pink-200 font-medium"
                                        )}>
                                            {t.type === 'thought' ? (
                                                <ThoughtContentRenderer content={t.content} />
                                            ) : (
                                                t.type === 'action' || t.content.startsWith('Calling ') ? translateAction(t.content) : t.content
                                            )}
                                        </div>
                                    </div>
                                </div>
                            ))}
                        </div>

                        {/* Life Chat Input */}
                        <div className="p-4 border-t border-zinc-800 bg-zinc-900/50 backdrop-blur-sm">
                            <div className="relative flex items-center gap-2">
                                <input
                                    type="text"
                                    value={chatInput}
                                    onChange={(e) => setChatInput(e.target.value)}
                                    onKeyDown={(e) => e.key === 'Enter' && handleSendToLife()}
                                    disabled={!lifeActive || isSending}
                                    placeholder={lifeActive ? "与 Life 小英对话..." : "Life Agent 未开启"}
                                    className="flex-1 bg-zinc-950 border border-zinc-800 rounded-md px-4 py-2 text-sm text-zinc-200 focus:outline-none focus:ring-1 focus:ring-purple-500/50 disabled:opacity-50"
                                />
                                <button
                                    onClick={handleSendToLife}
                                    disabled={!lifeActive || isSending || !chatInput.trim()}
                                    className="p-2 bg-purple-600 hover:bg-purple-700 text-white rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                                >
                                    <Send className="w-4 h-4" />
                                </button>
                            </div>
                        </div>
                    </>
                ) : (
                    <div className="p-6 space-y-6 font-mono text-sm">
                        <div className="space-y-4">
                             <div className="space-y-1">
                                <h3 className="text-zinc-100 font-medium text-lg">生命小英人设设定</h3>
                                <p className="text-zinc-500 text-xs">
                                    选择一个预设人设，或者自定义输入。
                                </p>
                            </div>

                            <div className="grid grid-cols-3 gap-3">
                                {presets.map((p) => (
                                    <button
                                        key={p.title}
                                        onClick={() => setPersona(p.content)}
                                        className={cn(
                                            "p-3 rounded-lg border text-left transition-all hover:border-purple-500/50 group",
                                            persona === p.content 
                                                ? "bg-purple-900/20 border-purple-500 ring-1 ring-purple-500/20" 
                                                : "bg-zinc-900/50 border-zinc-800 hover:bg-zinc-900"
                                        )}
                                    >
                                        <div className="font-medium text-zinc-200 mb-1 group-hover:text-purple-400 transition-colors">{p.title}</div>
                                        <div className="text-[10px] text-zinc-500 leading-snug line-clamp-2">
                                            {p.description}
                                        </div>
                                    </button>
                                ))}
                            </div>
                            
                            <div className="space-y-2">
                                <label className="text-xs text-zinc-400 font-medium uppercase tracking-wider">详细设定 Prompt</label>
                                <textarea
                                    value={persona}
                                    onChange={(e) => setPersona(e.target.value)}
                                    className="w-full h-40 bg-zinc-900/50 border border-zinc-800 rounded-md p-3 text-zinc-300 focus:outline-none focus:ring-1 focus:ring-purple-500/50 resize-none font-mono text-sm leading-relaxed"
                                    placeholder="输入自定义人设 Prompt..."
                                />
                            </div>

                            <div className="flex justify-end pt-2">
                                <button
                                    onClick={savePersona}
                                    disabled={isLoading}
                                    className="flex items-center gap-2 px-4 py-2 bg-purple-600 hover:bg-purple-700 text-white rounded-md transition-colors disabled:opacity-50 text-sm font-medium shadow-lg shadow-purple-900/20"
                                >
                                    <Save className="w-4 h-4" />
                                    {isLoading ? '保存中...' : '保存设定'}
                                </button>
                            </div>
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
}
