import requests
import json
import time
import sys

BASE_URL = "http://localhost:3000"

def run_chat(session_id, messages, description=""):
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    data = {
        "model": "gpt-4o",
        "messages": messages,
        "stream": False,
        "session_id": session_id,
        "temperature": 0.0
    }
    
    try:
        print(f"\n{'='*60}")
        print(f"[测试] {description}")
        print(f"{'='*60}")
        
        start = time.time()
        response = requests.post(url, headers=headers, data=json.dumps(data))
        duration = time.time() - start
        
        if response.status_code != 200:
            print(f"[失败] HTTP {response.status_code}")
            print(f"  响应: {response.text[:200]}")
            return None, False

        result = response.json()
        
        if "error" in result:
            print(f"[失败] API 错误: {result['error']}")
            return None, False
        
        usage = result.get("usage", {})
        print(f"[成功] 耗时: {duration:.2f}s")
        print(f"  输入 tokens: {usage.get('prompt_tokens', 0)}")
        print(f"  输出 tokens: {usage.get('completion_tokens', 0)}")
        
        content = result["choices"][0]["message"]["content"]
        print(f"  回复预览: {content[:100]}...")
        
        return result, True
        
    except Exception as e:
        print(f"[异常] {type(e).__name__}: {e}")
        return None, False

def test_valid_utf8(session_id):
    print("\n" + "="*60)
    print("测试组 1: 有效 UTF-8 编码")
    print("="*60)
    
    messages = [
        {"role": "user", "content": "你好，这是一个正常的中文测试。Hello, this is a normal English test. 日本語テスト。한국어 테스트."}
    ]
    return run_chat(session_id, messages, "有效 UTF-8 (中英日韩混合)")

def test_gbk_encoded(session_id):
    print("\n" + "="*60)
    print("测试组 2: GBK 编码内容")
    print("="*60)
    
    gbk_text = "这是GBK编码的中文文本"
    try:
        gbk_bytes = gbk_text.encode('gbk')
        gbk_as_utf8 = gbk_bytes.decode('latin-1')
        
        messages = [
            {"role": "user", "content": f"请处理这段内容: {gbk_as_utf8}"}
        ]
        return run_chat(session_id, messages, "GBK 编码作为 Latin-1 解码 (模拟乱码)")
    except Exception as e:
        print(f"[跳过] GBK 编码测试失败: {e}")
        return None, False

def test_big5_encoded(session_id):
    print("\n" + "="*60)
    print("测试组 3: BIG5 编码内容")
    print("="*60)
    
    big5_text = "這是繁體中文BIG5編碼"
    try:
        big5_bytes = big5_text.encode('big5')
        big5_as_utf8 = big5_bytes.decode('latin-1')
        
        messages = [
            {"role": "user", "content": f"请处理这段内容: {big5_as_utf8}"}
        ]
        return run_chat(session_id, messages, "BIG5 编码作为 Latin-1 解码 (模拟乱码)")
    except Exception as e:
        print(f"[跳过] BIG5 编码测试失败: {e}")
        return None, False

def test_invalid_utf8_sequences(session_id):
    print("\n" + "="*60)
    print("测试组 4: 无效 UTF-8 字节序列")
    print("="*60)
    
    invalid_sequences = [
        b'\xff\xfe',
        b'\x80\x81\x82',
        b'\xc0\xaf',
        b'\xed\xa0\x80',
        b'\xf4\x90\x80\x80',
    ]
    
    results = []
    for i, seq in enumerate(invalid_sequences):
        try:
            content = seq.decode('latin-1')
            messages = [
                {"role": "user", "content": f"测试无效序列 {i+1}: {content}"}
            ]
            _, success = run_chat(session_id, messages, f"无效 UTF-8 序列 #{i+1} (字节: {seq.hex()})")
            results.append(success)
        except Exception as e:
            print(f"[跳过] 序列 {i+1}: {e}")
            results.append(False)
    
    return None, all(results)

def test_binary_like_content(session_id):
    print("\n" + "="*60)
    print("测试组 5: 类二进制内容")
    print("="*60)
    
    binary_samples = [
        bytes([0x00, 0x01, 0x02, 0x03, 0x04, 0x05]),
        bytes([0xFF, 0xFE, 0xFD, 0xFC, 0xFB]),
        bytes(range(256))[:100],
    ]
    
    results = []
    for i, sample in enumerate(binary_samples):
        try:
            content = sample.decode('latin-1')
            messages = [
                {"role": "user", "content": f"处理二进制数据 {i+1}: {content}"}
            ]
            _, success = run_chat(session_id, messages, f"类二进制内容 #{i+1}")
            results.append(success)
        except Exception as e:
            print(f"[跳过] 样本 {i+1}: {e}")
            results.append(False)
    
    return None, all(results)

def test_control_characters(session_id):
    print("\n" + "="*60)
    print("测试组 6: 控制字符")
    print("="*60)
    
    control_chars = "".join([chr(i) for i in range(32) if i not in [9, 10, 13]])
    
    messages = [
        {"role": "user", "content": f"包含控制字符的消息: A{control_chars}B"}
    ]
    return run_chat(session_id, messages, "控制字符 (0x00-0x1F)")

def test_mixed_encoding(session_id):
    print("\n" + "="*60)
    print("测试组 7: 混合编码内容")
    print("="*60)
    
    parts = []
    
    parts.append("正常UTF-8: 你好世界")
    
    gbk_bytes = "GBK中文".encode('gbk')
    parts.append(gbk_bytes.decode('latin-1'))
    
    parts.append("正常英文: Hello World")
    
    invalid_bytes = b'\xff\xfe\x80\x81'
    parts.append(invalid_bytes.decode('latin-1'))
    
    mixed_content = " | ".join(parts)
    
    messages = [
        {"role": "user", "content": f"请处理这段混合内容: {mixed_content}"}
    ]
    return run_chat(session_id, messages, "混合编码 (UTF-8 + GBK + 无效字节)")

def test_surrogate_pairs(session_id):
    print("\n" + "="*60)
    print("测试组 8: 孤立的 UTF-16 代理对")
    print("="*60)
    
    surrogate_samples = [
        chr(0xD800),
        chr(0xDC00),
        chr(0xDFFF),
        chr(0xD800) + "A" + chr(0xDC00),
    ]
    
    results = []
    for i, surrogate in enumerate(surrogate_samples):
        try:
            messages = [
                {"role": "user", "content": f"代理对测试 {i+1}: {surrogate}"}
            ]
            _, success = run_chat(session_id, messages, f"孤立代理对 #{i+1}")
            results.append(success)
        except Exception as e:
            print(f"[跳过] 代理对 {i+1}: {e}")
            results.append(False)
    
    return None, all(results)

def test_long_invalid_content(session_id):
    print("\n" + "="*60)
    print("测试组 9: 长无效内容")
    print("="*60)
    
    invalid_byte = b'\xff'
    long_invalid = (invalid_byte * 1000).decode('latin-1')
    
    messages = [
        {"role": "user", "content": f"长无效内容测试: {long_invalid[:100]}... (共 {len(long_invalid)} 字符)"}
    ]
    return run_chat(session_id, messages, "长无效内容 (1000个无效字节)")

def test_null_bytes(session_id):
    print("\n" + "="*60)
    print("测试组 10: 空字节")
    print("="*60)
    
    null_content = "Before" + "\x00" * 10 + "After"
    
    messages = [
        {"role": "user", "content": f"空字节测试: {null_content}"}
    ]
    return run_chat(session_id, messages, "嵌入空字节")

def test_emoji_and_special(session_id):
    print("\n" + "="*60)
    print("测试组 11: Emoji 和特殊 Unicode")
    print("="*60)
    
    special_chars = "😀🎉🚀 中文 𝕳𝖊𝖑𝖑𝖔 こんにちは 안녕하세요 مرحبا שלום"
    
    messages = [
        {"role": "user", "content": f"特殊字符测试: {special_chars}"}
    ]
    return run_chat(session_id, messages, "Emoji 和多语言特殊字符")

def test_file_path_like(session_id):
    print("\n" + "="*60)
    print("测试组 12: 类文件路径内容 (可能触发工具)")
    print("="*60)
    
    messages = [
        {"role": "user", "content": "请读取文件 C:\\test\\乱码测试.txt 的内容"}
    ]
    return run_chat(session_id, messages, "类文件路径请求")

def test_error_message_simulation(session_id):
    print("\n" + "="*60)
    print("测试组 13: 模拟错误消息")
    print("="*60)
    
    error_like = "Error: \xff\xfe at position 0x8080"
    
    messages = [
        {"role": "user", "content": f"这是一个错误消息: {error_like}"}
    ]
    return run_chat(session_id, messages, "模拟包含无效字节的错误消息")

def main():
    print("="*60)
    print("编码鲁棒性测试套件")
    print("="*60)
    print(f"服务地址: {BASE_URL}")
    print(f"测试时间: {time.strftime('%Y-%m-%d %H:%M:%S')}")
    
    session_id = "encoding-test-" + str(int(time.time()))
    print(f"会话 ID: {session_id}")
    
    test_results = []
    
    tests = [
        ("有效 UTF-8", test_valid_utf8),
        ("GBK 编码", test_gbk_encoded),
        ("BIG5 编码", test_big5_encoded),
        ("无效 UTF-8 序列", test_invalid_utf8_sequences),
        ("类二进制内容", test_binary_like_content),
        ("控制字符", test_control_characters),
        ("混合编码", test_mixed_encoding),
        ("孤立代理对", test_surrogate_pairs),
        ("长无效内容", test_long_invalid_content),
        ("空字节", test_null_bytes),
        ("Emoji 和特殊字符", test_emoji_and_special),
        ("类文件路径", test_file_path_like),
        ("模拟错误消息", test_error_message_simulation),
    ]
    
    for name, test_func in tests:
        try:
            _, success = test_func(session_id)
            test_results.append((name, success))
        except Exception as e:
            print(f"\n[测试异常] {name}: {e}")
            test_results.append((name, False))
        
        time.sleep(0.5)
    
    print("\n" + "="*60)
    print("测试结果汇总")
    print("="*60)
    
    passed = 0
    failed = 0
    
    for name, success in test_results:
        status = "✓ 通过" if success else "✗ 失败"
        print(f"  {status}: {name}")
        if success:
            passed += 1
        else:
            failed += 1
    
    print(f"\n总计: {passed} 通过, {failed} 失败")
    print(f"成功率: {passed/(passed+failed)*100:.1f}%")
    
    if failed > 0:
        print("\n[警告] 部分测试失败，请检查编码处理逻辑")
        sys.exit(1)
    else:
        print("\n[成功] 所有测试通过！编码处理鲁棒性良好")
        sys.exit(0)

if __name__ == "__main__":
    main()
