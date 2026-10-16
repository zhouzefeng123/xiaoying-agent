import { useState } from 'react';
import { useSwarm, type MultiAgentEvent, type ToolCall } from '../../hooks/use-swarm';
import { Play, Loader2, Users } from 'lucide-react';
import { cn } from '../../lib/utils';
import { toast } from 'sonner';

export function SimpleTaskLauncher() {
    const { events, agents, isConnected } = useSwarm();
    const [task, setTask] = useState('');
    const [isExecuting, setIsExecuting] = useState(false);
    const [progress, setProgress] = useState<string[]>([]);

    const handleExecute = async () => {
        if (!task.trim()) return;
        
        setIsExecuting(true);
        setProgress(prev => [...prev, `📋 Submitting Task: "${task}"`]);
        
        try {
            // Call the new "High Speed Execution" endpoint
            const res = await fetch('/v1/swarm/execute', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ task })
            });

            if (!res.ok) throw new Error('Failed to submit task');
            
            toast.success('Task submitted for intelligent execution!');
            setTask('');
        } catch (error) {
            console.error(error);
            toast.error('Failed to submit task');
        } finally {
            setTimeout(() => setIsExecuting(false), 1000);
        }
    };

    return (
        <div className="flex flex-col h-full bg-zinc-950 text-zinc-300 font-mono text-sm overflow-hidden rounded-lg border border-zinc-800">
            {/* Header */}
            <div className="flex items-center justify-between p-4 border-b border-zinc-800 bg-zinc-900/50">
                <div className="flex items-center gap-3">
                    <div className="p-2 bg-purple-500/10 rounded-lg">
                        <Users className="w-5 h-5 text-purple-400" />
                    </div>
                    <div>
                        <h2 className="font-bold text-zinc-100">Swarm Task Force</h2>
                        <p className="text-xs text-zinc-500">Autonomous Multi-Agent Planning & Execution</p>
                    </div>
                </div>
                <div className="flex items-center gap-2 text-xs">
                    <span className={cn("flex items-center gap-1.5 px-2 py-1 rounded-full border", 
                        isConnected ? "bg-emerald-500/10 border-emerald-500/20 text-emerald-400" : "bg-red-500/10 border-red-500/20 text-red-400")}>
                        <div className={cn("w-1.5 h-1.5 rounded-full", isConnected ? "bg-emerald-500 animate-pulse" : "bg-red-500")} />
                        {isConnected ? "Online" : "Offline"}
                    </span>
                    <span className="px-2 py-1 bg-zinc-900 border border-zinc-800 rounded-full text-zinc-400">
                        {agents.length} Agents Active
                    </span>
                </div>
            </div>

            {/* Main Content */}
            <div className="flex-1 flex flex-col p-6 gap-6 overflow-hidden">
                
                {/* Task Input */}
                <div className="space-y-2 shrink-0">
                    <label className="text-xs font-bold text-zinc-400 uppercase tracking-wider">Mission Objective</label>
                    <div className="flex gap-2">
                        <input 
                            className="flex-1 bg-zinc-900/50 border border-zinc-800 rounded-lg px-4 py-3 focus:border-purple-500 focus:ring-1 focus:ring-purple-500/20 outline-none text-zinc-100 placeholder:text-zinc-600 transition-all"
                            placeholder="e.g. Analyze the project structure and summarize key components..."
                            value={task}
                            onChange={e => setTask(e.target.value)}
                            onKeyDown={e => e.key === 'Enter' && handleExecute()}
                            disabled={isExecuting}
                        />
                        <button 
                            onClick={handleExecute}
                            disabled={!task.trim() || isExecuting}
                            className="px-6 bg-purple-600 hover:bg-purple-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg font-medium transition-all flex items-center gap-2 shadow-lg shadow-purple-900/20"
                        >
                            {isExecuting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4 fill-current" />}
                            Execute
                        </button>
                    </div>
                </div>

                {/* Progress & Live Feed */}
                <div className="flex-1 flex gap-4 min-h-0">
                    
                    {/* Status Log */}
                    <div className="w-1/3 bg-zinc-900/30 border border-zinc-800 rounded-lg p-4 flex flex-col">
                        <h3 className="text-xs font-bold text-zinc-500 uppercase tracking-wider mb-3">System Status</h3>
                        <div className="flex-1 overflow-y-auto space-y-2 pr-2 scrollbar-thin scrollbar-thumb-zinc-800">
                            {progress.map((msg, i) => (
                                <div key={i} className="text-xs text-zinc-400 flex items-start gap-2 animate-in fade-in slide-in-from-left-2">
                                    <span className="text-zinc-600 mt-0.5">›</span>
                                    <span>{msg}</span>
                                </div>
                            ))}
                        </div>
                    </div>

                    {/* Agent Activity Stream */}
                    <div className="flex-1 bg-black border border-zinc-800 rounded-lg p-4 flex flex-col">
                        <h3 className="text-xs font-bold text-zinc-500 uppercase tracking-wider mb-3 flex justify-between items-center">
                            <span>Live Swarm Activity</span>
                            <span className="text-[10px] bg-zinc-900 px-2 py-0.5 rounded text-zinc-500">Real-time</span>
                        </h3>
                        <div className="flex-1 overflow-y-auto space-y-4 pr-2 scrollbar-thin scrollbar-thumb-zinc-800">
                            {events.slice(-50).map((e: MultiAgentEvent, i: number) => (
                                <div key={i} className="flex gap-3 animate-in fade-in slide-in-from-bottom-2">
                                    <div className="w-24 shrink-0 text-right">
                                        <div className={cn(
                                            "text-xs font-bold truncate",
                                            e.From?.startsWith('manager') ? "text-purple-400" :
                                            e.From?.startsWith('researcher') ? "text-blue-400" :
                                            e.From?.startsWith('coder') ? "text-yellow-400" :
                                            e.From?.startsWith('qa') ? "text-green-400" :
                                            "text-zinc-500"
                                        )}>
                                            {e.From?.split('-')[0] || 'System'}
                                        </div>
                                    </div>
                                    <div className="flex-1 min-w-0">
                                        {e.Type === 'message' && (
                                            <div className="text-zinc-300 whitespace-pre-wrap bg-zinc-900/50 p-2 rounded border border-zinc-800/50">
                                                {e.Content}
                                            </div>
                                        )}
                                        {e.Type === 'tool_call' && (
                                            <div className="text-xs text-zinc-500 flex items-center gap-2">
                                                <span className="text-purple-400">⚡ Action:</span>
                                                {e.ToolCalls?.map((t: ToolCall) => `${t.name}`).join(', ')}
                                            </div>
                                        )}
                                        {e.Type === 'tool_result' && (
                                            <div className="text-xs text-zinc-500 flex items-center gap-2">
                                                <span className="text-green-400">✓ Result:</span>
                                                <span className="truncate max-w-[300px]">{e.ToolResult?.content}</span>
                                            </div>
                                        )}
                                    </div>
                                </div>
                            ))}
                            {events.length === 0 && (
                                <div className="h-full flex items-center justify-center text-zinc-700 text-xs italic">
                                    Waiting for mission...
                                </div>
                            )}
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}
