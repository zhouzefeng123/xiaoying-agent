param(
    [string]$InputFile
)

Add-Type -AssemblyName System.Speech

$engine = New-Object System.Speech.Recognition.SpeechRecognitionEngine
$engine.SetInputToWaveFile($InputFile)

# Load a default dictation grammar
$grammar = New-Object System.Speech.Recognition.DictationGrammar
$engine.LoadGrammar($grammar)

# Recognize
try {
    $result = $engine.Recognize()
    if ($result -ne $null) {
        Write-Output $result.Text
    } else {
        Write-Output ""
    }
} catch {
    Write-Error $_.Exception.Message
} finally {
    $engine.Dispose()
}
