// PDF.js follows Mozilla's document/page rendering API.
// https://mozilla.github.io/pdf.js/examples/
async function receiptImageDataURL(source) {
    const image = new Image();
    image.src = source;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    canvas.getContext('2d').drawImage(image, 0, 0);
    return canvas.toDataURL('image/png');
}

async function readReceiptFile(file, progress) {
    if (file.size > 10 * 1024 * 1024) throw new Error('Choose a receipt smaller than 10 MiB.');
    const recognize = async image => {
        if (!window.Tesseract) throw new Error('The OCR engine could not load. Check your connection and try again.');
        return (await Tesseract.recognize(image, 'eng')).data.text;
    };
    if (file.type === 'application/pdf' || file.name.toLowerCase().endsWith('.pdf')) {
        progress('Reading PDF…', 30);
        const pdfjs = await import('https://cdn.jsdelivr.net/npm/pdfjs-dist@4.10.38/build/pdf.min.mjs');
        pdfjs.GlobalWorkerOptions.workerSrc = 'https://cdn.jsdelivr.net/npm/pdfjs-dist@4.10.38/build/pdf.worker.min.mjs';
        const task = pdfjs.getDocument({data:new Uint8Array(await file.arrayBuffer()), isEvalSupported:false});
        const pdf = await task.promise;
        try {
            if (pdf.numPages > 10) throw new Error('Choose a receipt PDF with no more than 10 pages.');
            let text = '', preview = '';
            for (let number = 1; number <= pdf.numPages; number++) {
                progress(`Reading page ${number} of ${pdf.numPages}…`, 30 + Math.round(number / pdf.numPages * 50));
                const page = await pdf.getPage(number);
                const content = await page.getTextContent();
                let pageText = content.items.map(item => (item.str || '') + (item.hasEOL ? '\n' : ' ')).join('');
                if (number === 1 || !pageText.trim()) {
                    const base = page.getViewport({scale:1});
                    const viewport = page.getViewport({scale:Math.min(2, 2000 / Math.max(base.width,base.height))});
                    const canvas = document.createElement('canvas');
                    canvas.width = Math.ceil(viewport.width); canvas.height = Math.ceil(viewport.height);
                    await page.render({canvasContext:canvas.getContext('2d'),viewport}).promise;
                    if (number === 1) preview = canvas.toDataURL('image/png');
                    if (!pageText.trim()) pageText = await recognize(canvas);
                }
                text += pageText + '\n';
                page.cleanup();
            }
            return {text, preview};
        } finally { await pdf.destroy(); }
    }
    if (!file.type.startsWith('image/')) throw new Error('Choose an image or PDF receipt.');
    const url = URL.createObjectURL(file);
    try {
        const preview = await receiptImageDataURL(url);
        progress('Reading receipt text…', 50);
        return {text:await recognize(preview), preview};
    } finally { URL.revokeObjectURL(url); }
}
