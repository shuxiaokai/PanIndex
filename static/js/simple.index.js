$(document).ready(function() {
    var clipboard = new ClipboardJS('.copyBtn', {
        text: function(trigger) {
            var path = $(trigger).data("path");
            var fullUrl = window.location.protocol + "//"+window.location.host + path;
            return encodeURI(fullUrl);
        }
    });
    clipboard.on('success', function(e) {
        Swal.fire({
            position: 'top',
            text: '链接已复制到剪切板',
            showConfirmButton: false,
            timer: 1500
        })
        e.clearSelection();
    });
    $('.icon-dir').on('click', function(ev) {
        if(ev.target.title == "下载" || ev.target.title == "复制链接") return ;
        var dURL = $(this).attr("data-url");
        var fullUrl = window.location.protocol+"//"+window.location.host + dURL;
        window.location.href = fullUrl;
    });
    $('.icon-file').on('click', function(ev) {
        if(ev.target.title == "下载" || ev.target.title == "复制链接") return;
        var dURL = $(this).attr("data-url");
        var title = $(this).attr("data-title");
        var dvt = $(this).attr("data-view-type");
        var preview = $(this).attr("data-preview");
        var fileType = $(this).attr("data-file-type");
        var fullUrl = window.location.protocol+"//"+window.location.host + dURL;
        if(preview == "0"){
            window.location.href = fullUrl;
            return false;
        }
        if(dvt == "img"){
            const image = new Image();
            image.src = fullUrl;
            const viewer = new Viewer(image, {
                hidden: function () {
                    viewer.destroy();
                },
            });
            viewer.show();
            return;
        }else if(dvt == "video"){
            var art;
            Swal.fire({
                template: '#video-modal',
                html: '<div class="artplayer-app" style="width: 100%;height: 33.75rem"></div>',
                width: "60rem",
                showConfirmButton: false,
                allowOutsideClick: true,
                allowEscapeKey: false,
                allowEnterKey: false,
                showCancelButton: false,
                showCloseButton: false,
                backdrop: true,
                didOpen: () => {
                    var qas = buildOriginalVideo(fullUrl, fileType);
                    art = initVideo(".artplayer-app", qas, title)
                },
                willClose: () => {
                    art.destroy();
                }
            });
            return;
        }else if(dvt == "audio"){
            const ap = new APlayer({
                container: document.getElementById('aplayer'),
                fixed: true,
                lrcType: 3,
                autoplay: true,
                audio: [{
                    name: title,
                    artist: 'artist',
                    url: dURL,
                    cover: '/static/img/music-cover.png',
                    lrc: dURL.split('.')[0] + '.lrc'
                }]
            });
            return;
        }
        if(fileType == "doc" || fileType == "docx" || fileType == "dotx"
            || fileType == "ppt" || fileType == "pptx" || fileType == "xls" || fileType == "xlsx"){
            window.open("https://view.officeapps.live.com/op/view.aspx?src="+fullUrl);
        }else{
            window.location.href = dURL;
        }
    });
    $('.download_btn').on('click', function(ev) {
        window.location.href = $(this).attr("data-url");
        ev.preventDefault();
    });
    $(".search").on("keydown", function(event) {
        var accountHome = $(this).attr("data-index");
        var key = $(this).val();
        key = key.replace(/(^\s*)|(\s*$)/g,"")
        if (event.key === "Enter") {
            if( $(this).val() != ""){
                window.location.href = "/?search=" + key;
            }else{
                window.location.href = accountHome;
            }
        }
    });
    $('.table-head').on('click', function() {
        var orderColumn = $(this).text();
        var orderSeq = $(this).attr("data-order-seq");
        var orderType = $(this).attr("data-order-type");
        $('.table-head').each(function(){
            $(this).text($(this).text());
        });
        if(orderSeq == "" || orderSeq == "down"){
            //当前是升序排列，按照orderColumn降序
            sortTable("up", orderType);
            $(this).attr("data-order-seq", "up");
            $(this).html(orderColumn+" <i class=\"fa fa-angle-double-up\" aria-hidden=\"true\"></i>");
        }else if(orderSeq == "up"){
            sortTable("down", orderType);
            $(this).attr("data-order-seq", "down");
            $(this).html(orderColumn+" <i class=\"fa fa-angle-double-down\" aria-hidden=\"true\"></i>");
        }
    });
    $(".search").bind('keydown', function(event) {
        var key = $(this).val();
        key = key.replace(/(^\s*)|(\s*$)/g,"")
        if (event.key === "Enter") {
            if( $(this).val() != ""){
                window.location.href = "/?search=" + key;
            }
        }
    });
});
function promptPwd(fullPath, errMsg){
    var errorMsg = errMsg;
    if(errorMsg == ""){
        errorMsg = "这是一个受保护的文件夹，您需要提供访问密码才能查看。";
    }else{
        //remove err pwd
        removePwd(md5(fullPath))
    }
    var pwd = prompt(errorMsg);
    if(pwd && pwd != "" && pwd != null && pwd != "null"){
        var result = Cookies.get("file_pwd");
        var ppwd = md5(fullPath) + ":" + pwd;
        if (result && result != null && result != "null" && result != ""){
            result = result + ","+ ppwd;
        }else{
            result = ppwd;
        }
        Cookies.set("file_pwd", result, { expires: 3650, path: '/' });
    }
    location.reload();
}
function removePwd(pathMd5){
    var newFilePwd = [];
    var cookiePwd = Cookies.get("file_pwd");
    if(cookiePwd && cookiePwd != "" && cookiePwd != null && cookiePwd != "null"){
        $.each(cookiePwd.split(","), function (i, item){
            var pMd5 = item.split(":")[0]
            if(pMd5 != pathMd5){
                newFilePwd.push(item);
            }
        });
        if (newFilePwd.length > 0){
            Cookies.set("file_pwd", newFilePwd.join(","), {expires : 3650, path:"/"});
        }else{
            Cookies.remove('file_pwd', { path: '/' });
        }
    }

}
$.fn.extend({
    sortElements: function (comparator, getSortable) {
        getSortable = getSortable || function () { return this; };

        var placements = this.map(function () {
            var sortElement = getSortable.call(this),
                parentNode = sortElement.parentNode,
                nextSibling = parentNode.insertBefore(
                    document.createTextNode(''),
                    sortElement.nextSibling
                );

            return function () {
                parentNode.insertBefore(this, nextSibling);
                parentNode.removeChild(nextSibling);
            };
        });

        return [].sort.call(this, comparator).each(function (i) {
            placements[i].call(getSortable.call(this));
        });
    }
});
function sortTable(sort_order, data_type){
    $('table tbody > tr').not('.parent').sortElements(function (a, b) {
        let data_a = $(a).find("td[class='"+data_type+"']").text(), data_b = $(b).find("td[class='"+data_type+"']").text();
        let rt = data_a.localeCompare(data_b);
        return (sort_order === "down") ? 0-rt : rt;
    });
}