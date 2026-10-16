import { useState, useEffect, useRef, useMemo } from 'react';
import { X, Database, Zap, Plus, Search, Filter, Save, Edit2, Share2, Ban } from 'lucide-react';
import { cn } from '../../lib/utils';
import { toast } from 'sonner';
import ForceGraph2D, { type ForceGraphMethods } from 'react-force-graph-2d';
import { memoryService } from '../../services/memoryService';

interface MemoryVizModalProps {
    isOpen: boolean;
    onClose: () => void;
}

// Graph Data Types
type Node = {
    id: string;
    label: string;
    group: string; // category
    val: number; // size based on importance/access
    color: string;
    content: string; // full content
    neighbors?: string[]; // IDs of neighbors
    links?: Link[]; // Connected links
    x?: number;
    y?: number;
};

type Link = {
    source: string | Node; // ForceGraph might replace string with Node object
    target: string | Node;
    value: number; // weight
};

type GraphData = {
    nodes: Node[];
    links: Link[];
};

export function MemoryVizModal({ isOpen, onClose }: MemoryVizModalProps) {
    type Memory = {
        id: string;
        category?: string;
        scope?: string;
        content: string;
    };

    const [memories, setMemories] = useState<Memory[]>([]);
    const [loading, setLoading] = useState(true);
    const [graphData, setGraphData] = useState<GraphData>({ nodes: [], links: [] });
    const fgRef = useRef<ForceGraphMethods | undefined>(undefined);
    const [filter, setFilter] = useState('');
    const [selectedNode, setSelectedNode] = useState<Node | null>(null);
    
    // Editing State
    const [isEditing, setIsEditing] = useState(false);
    const [editContent, setEditContent] = useState('');
    const [isSaving, setIsSaving] = useState(false);

    // Highlight State
    const [highlightNodes, setHighlightNodes] = useState<Set<string>>(new Set());
    const [highlightLinks, setHighlightLinks] = useState<Set<Link>>(new Set());
    const [isHighlightActive, setIsHighlightActive] = useState(false);

    useEffect(() => {
        if (isOpen) {
            loadMemories();
        }
    }, [isOpen]);

    // Reset edit state when node changes
    useEffect(() => {
        if (selectedNode) {
            setEditContent(selectedNode.content);
            setIsEditing(false);
            // Reset highlight when selecting a new node manually? 
            // Maybe keep it if it was a neighbor click.
            // For simplicity, let's keep highlight manual.
        }
    }, [selectedNode]);

    const loadMemories = async () => {
        setLoading(true);
        try {
            // Use the new graph endpoint
            const res = await fetch('/v1/memories/graph');
            if (res.ok) {
                const data = await res.json();
                
                const rawNodes: Memory[] = data.nodes || [];
                const rawLinks: any[] = data.links || [];

                setMemories(rawNodes);
                
                // Transform to Graph Data
                const nodes: Node[] = rawNodes.map(m => ({
                    id: m.id,
                    label: m.content.length > 20 ? m.content.substring(0, 20) + '...' : m.content,
                    content: m.content,
                    group: m.category || 'unknown',
                    val: m.category === 'instruction' ? 8 : m.category === 'preference' ? 6 : 4,
                    color: getNodeColor(m.category),
                    neighbors: [],
                    links: []
                }));

                // Process links to build neighbor map for fast lookup
                const links: Link[] = rawLinks.map(l => ({
                    source: l.source,
                    target: l.target,
                    value: l.value
                }));

                // Populate neighbors (naive approach, better done with a map)
                links.forEach(link => {
                    const sourceId = typeof link.source === 'object' ? (link.source as any).id : link.source;
                    const targetId = typeof link.target === 'object' ? (link.target as any).id : link.target;

                    const sourceNode = nodes.find(n => n.id === sourceId);
                    const targetNode = nodes.find(n => n.id === targetId);

                    if (sourceNode && targetNode) {
                        sourceNode.neighbors = sourceNode.neighbors || [];
                        sourceNode.neighbors.push(targetId);
                        targetNode.neighbors = targetNode.neighbors || [];
                        targetNode.neighbors.push(sourceId);
                    }
                });

                setGraphData({ nodes, links });
            }
        } catch (e) {
            console.error(e);
            toast.error("Failed to load memory graph");
        } finally {
            setLoading(false);
        }
    };

    const getNodeColor = (category?: string) => {
        switch (category) {
            case 'instruction': return '#ef4444'; // Red-500
            case 'experience': return '#f97316'; // Orange-500
            case 'preference': return '#a855f7'; // Purple-500
            case 'habit': return '#22c55e'; // Green-500
            case 'fact': return '#3b82f6'; // Blue-500
            default: return '#71717a'; // Zinc-500
        }
    };

    const handleSave = async () => {
        if (!selectedNode) return;
        setIsSaving(true);
        try {
            await memoryService.update(selectedNode.id, editContent, selectedNode.group);
            toast.success("记忆已更新");
            setIsEditing(false);
            
            // Update local graph data
            setGraphData(prev => ({
                ...prev,
                nodes: prev.nodes.map(n => n.id === selectedNode.id ? { ...n, content: editContent, label: editContent.length > 20 ? editContent.substring(0, 20) + '...' : editContent } : n)
            }));
            // Update selected node ref
            setSelectedNode(prev => prev ? { ...prev, content: editContent } : null);
        } catch (e) {
            toast.error("更新失败");
        } finally {
            setIsSaving(false);
        }
    };

    const handleFindRelated = () => {
        if (!selectedNode) return;
        
        if (isHighlightActive) {
            // Clear highlight
            setHighlightNodes(new Set());
            setHighlightLinks(new Set());
            setIsHighlightActive(false);
            return;
        }

        // Activate highlight
        const neighbors = new Set<string>();
        neighbors.add(selectedNode.id);
        
        // Find links connected to this node
        // Note: react-force-graph modifies links to be objects, so we need to handle that
        const relatedLinks = new Set<Link>();
        
        graphData.links.forEach(link => {
            const sourceId = typeof link.source === 'object' ? (link.source as any).id : link.source;
            const targetId = typeof link.target === 'object' ? (link.target as any).id : link.target;

            if (sourceId === selectedNode.id || targetId === selectedNode.id) {
                relatedLinks.add(link);
                neighbors.add(sourceId);
                neighbors.add(targetId);
            }
        });

        setHighlightNodes(neighbors);
        setHighlightLinks(relatedLinks);
        setIsHighlightActive(true);
        
        // Zoom to fit
        if (fgRef.current) {
            // fgRef.current.zoomToFit(1000, 50, (node: any) => neighbors.has(node.id));
            // Just focus on the node again
            fgRef.current.centerAt(selectedNode.x as number, selectedNode.y as number, 1000);
            fgRef.current.zoom(6, 1000);
        }
    };

    if (!isOpen) return null;

    return (
        <div className="fixed inset-0 z-[100] bg-black/80 backdrop-blur-sm flex items-center justify-center">
            <div className="w-[90vw] h-[85vh] bg-zinc-900 border border-zinc-800 rounded-xl shadow-2xl flex flex-col overflow-hidden relative">
                {/* Header */}
                <div className="absolute top-0 left-0 right-0 z-10 flex items-center justify-between p-4 bg-gradient-to-b from-zinc-900 to-transparent pointer-events-none">
                    <div className="flex items-center gap-3 pointer-events-auto">
                        <div className="flex items-center gap-2 text-zinc-100 font-bold text-lg bg-zinc-900/80 px-3 py-1.5 rounded-full border border-zinc-800 backdrop-blur">
                            <Database className="w-5 h-5 text-purple-500" />
                            <span className="text-zinc-500 text-sm font-normal ml-1">{graphData.nodes.length} 个节点</span>
                        </div>
                        
                        {/* Legend */}
                        <div className="flex gap-2 text-[10px] text-zinc-500 bg-zinc-900/80 px-3 py-2 rounded-full border border-zinc-800 backdrop-blur">
                            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-red-500"></span>规则 (Rule)</span>
                            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-orange-500"></span>经验 (Exp)</span>
                            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-purple-500"></span>偏好 (Pref)</span>
                            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-green-500"></span>习惯 (Habit)</span>
                            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-blue-500"></span>事实 (Fact)</span>
                        </div>
                    </div>

                    <div className="flex items-center gap-2 pointer-events-auto">
                        <button onClick={onClose} className="p-2 hover:bg-zinc-800 rounded-full text-zinc-400 hover:text-zinc-100 transition-colors bg-zinc-900/80 border border-zinc-800 z-50 cursor-pointer">
                            <X className="w-6 h-6" />
                        </button>
                    </div>
                </div>

                {/* Graph Area */}
                <div className="flex-1 bg-zinc-950 relative cursor-move">
                    {loading ? (
                        <div className="absolute inset-0 flex items-center justify-center text-zinc-500 gap-2">
                            <div className="w-5 h-5 border-2 border-purple-500 border-t-transparent rounded-full animate-spin"></div>
                            加载知识图谱中...
                        </div>
                    ) : (
                        <ForceGraph2D
                            ref={fgRef}
                            graphData={graphData}
                            nodeLabel="label"
                            nodeColor={node => isHighlightActive ? (highlightNodes.has((node as any).id) ? (node as Node).color : '#3f3f46') : (node as Node).color}
                            nodeRelSize={6}
                            linkColor={link => isHighlightActive ? (highlightLinks.has(link as Link) ? '#a855f7' : '#18181b') : '#3f3f46'}
                            linkWidth={link => isHighlightActive ? (highlightLinks.has(link as Link) ? 2 : 0.5) : 1}
                            backgroundColor="#09090b" // zinc-950
                            onNodeClick={(node) => {
                                const n = node as Node;
                                setSelectedNode(n);
                                if (n.x !== undefined && n.y !== undefined) {
                                    fgRef.current?.centerAt(n.x, n.y, 1000);
                                    fgRef.current?.zoom(4, 2000);
                                }
                            }}
                            onBackgroundClick={() => {
                                setSelectedNode(null);
                                setIsHighlightActive(false);
                                setHighlightNodes(new Set());
                            }}
                            cooldownTicks={100}
                            d3AlphaDecay={0.02}
                            d3VelocityDecay={0.3}
                        />
                    )}
                </div>

                {/* Node Details Panel (Bottom Left Overlay) */}
                {selectedNode && (
                    <div className="absolute bottom-6 left-6 w-96 bg-zinc-900/90 backdrop-blur border border-zinc-700 rounded-xl p-5 shadow-2xl animate-in slide-in-from-bottom-10 fade-in duration-300">
                        <div className="flex items-start justify-between mb-4">
                            <div className="flex items-center gap-2">
                                <span className="w-3 h-3 rounded-full" style={{ backgroundColor: selectedNode.color }}></span>
                                <span className="text-xs font-bold uppercase text-zinc-400 tracking-wider">{selectedNode.group}</span>
                            </div>
                            <div className="flex items-center gap-2">
                                {isEditing ? (
                                    <button 
                                        onClick={() => setIsEditing(false)}
                                        className="text-zinc-500 hover:text-zinc-300 p-1 hover:bg-zinc-800 rounded"
                                    >
                                        <Ban className="w-4 h-4" />
                                    </button>
                                ) : null}
                                <button onClick={() => setSelectedNode(null)} className="text-zinc-500 hover:text-zinc-300 p-1 hover:bg-zinc-800 rounded">
                                    <X className="w-4 h-4" />
                                </button>
                            </div>
                        </div>

                        {isEditing ? (
                            <textarea 
                                className="w-full h-32 bg-zinc-950/50 border border-zinc-700 rounded-lg p-3 text-sm text-zinc-200 focus:outline-none focus:border-purple-500 resize-none"
                                value={editContent}
                                onChange={(e) => setEditContent(e.target.value)}
                            />
                        ) : (
                            <p className="text-zinc-100 text-sm leading-relaxed font-medium max-h-40 overflow-y-auto pr-2">
                                {selectedNode.content}
                            </p>
                        )}

                        <div className="mt-4 flex gap-2">
                            {isEditing ? (
                                <button 
                                    onClick={handleSave}
                                    disabled={isSaving}
                                    className="flex-1 bg-green-600 hover:bg-green-500 text-white text-xs py-2 rounded-lg transition-colors flex items-center justify-center gap-2"
                                >
                                    {isSaving ? <span className="animate-spin">⏳</span> : <Save className="w-3 h-3" />}
                                    保存修改
                                </button>
                            ) : (
                                <button 
                                    onClick={() => setIsEditing(true)}
                                    className="flex-1 bg-zinc-800 hover:bg-zinc-700 text-zinc-300 text-xs py-2 rounded-lg transition-colors border border-zinc-700 flex items-center justify-center gap-2"
                                >
                                    <Edit2 className="w-3 h-3" />
                                    编辑记忆
                                </button>
                            )}
                            
                            <button 
                                onClick={handleFindRelated}
                                className={cn(
                                    "flex-1 text-xs py-2 rounded-lg transition-colors border flex items-center justify-center gap-2",
                                    isHighlightActive 
                                        ? "bg-purple-500 text-white border-purple-500" 
                                        : "bg-purple-500/10 hover:bg-purple-500/20 text-purple-400 border-purple-500/30"
                                )}
                            >
                                <Share2 className="w-3 h-3" />
                                {isHighlightActive ? "显示全部" : "查找关联"}
                            </button>
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
}
